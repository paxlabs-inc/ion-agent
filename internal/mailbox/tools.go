package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

// Registrations exposes receive and private verification handoff through the
// shared production tool manager.
func (store *Store) Registrations(
	browserService *browser.Service,
) []tools.Registration {
	check := func(context.Context) error {
		if store == nil || strings.TrimSpace(store.Address()) == "" {
			return errors.New("dedicated agent mailbox is not configured")
		}
		return nil
	}
	registrations := []tools.Registration{
		{
			Name:        "agent_mailbox_status",
			Description: "Show the dedicated receive-only agent email address and pending verification count.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Timeout:     5 * time.Second, Check: check,
			Classification: tools.ClassificationGreen,
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.Marshal(map[string]any{
					"address": store.Address(), "receive_only": true,
					"pending_verifications": len(store.List(100)),
				})
			},
		},
		{
			Name:        "agent_mailbox_sync",
			Description: "Check the dedicated agent mailbox for new confirmation links or verification codes.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Timeout:     30 * time.Second, Check: check,
			Classification:          tools.ClassificationYellow,
			ExternallyCommunicating: true,
			Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				added, err := store.Sync(ctx)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{
					"new_verifications": added,
					"pending":           store.List(20),
				})
			},
		},
		{
			Name:        "agent_mailbox_list",
			Description: "List redacted pending verification metadata; codes and confirmation links remain private.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},
				"additionalProperties":false
			}`),
			Timeout: 5 * time.Second, Check: check,
			Classification: tools.ClassificationGreen,
			Handler: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Limit int `json:"limit"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{
					"verifications": store.List(input.Limit),
				})
			},
		},
	}
	if browserService == nil {
		return registrations
	}
	registrations = append(registrations, tools.Registration{
		Name:        "browser_apply_verification",
		Description: "Privately apply one mailbox verification code or confirmation link in the active browser after explicit human approval.",
		Parameters: json.RawMessage(`{
			"type":"object","required":["verification_id","expected_domain"],
			"properties":{
				"verification_id":{"type":"string","format":"uuid"},
				"expected_domain":{"type":"string","minLength":1,"maxLength":253},
				"ref":{"type":"string","pattern":"^p[0-9]{1,4}$"}
			},
			"additionalProperties":false
		}`),
		Timeout: 35 * time.Second, Check: check,
		Classification:          tools.ClassificationRed,
		ExternallyCommunicating: true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				VerificationID string `json:"verification_id"`
				ExpectedDomain string `json:"expected_domain"`
				Ref            string `json:"ref"`
			}
			if err := decodeToolInput(raw, &input); err != nil {
				return nil, err
			}
			id, err := uuid.Parse(input.VerificationID)
			if err != nil {
				return nil, errors.New("mailbox: invalid verification ID")
			}
			found, err := store.Peek(id)
			if err != nil {
				return nil, err
			}
			expected := strings.ToLower(strings.TrimSpace(input.ExpectedDomain))
			switch found.Kind {
			case "confirmation_link":
				if expected == "" || expected != found.TargetDomain {
					return nil, fmt.Errorf(
						"mailbox: expected domain does not match confirmation target %q",
						found.TargetDomain,
					)
				}
				if strings.TrimSpace(input.Ref) != "" {
					return nil, errors.New(
						"mailbox: confirmation links do not accept a field ref",
					)
				}
				snapshot, err := browserService.OpenVerification(ctx, found.Value)
				if err != nil {
					return nil, err
				}
				if err := store.MarkConsumed(id); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{
					"applied": true, "kind": found.Kind,
					"target_domain": found.TargetDomain,
					"page": map[string]string{
						"url": snapshot.URL, "title": snapshot.Title,
					},
				})
			case "verification_code":
				if expected == "" {
					return nil, errors.New("mailbox: expected site domain is required")
				}
				if strings.TrimSpace(input.Ref) == "" {
					return nil, errors.New(
						"mailbox: verification code requires a browser field ref",
					)
				}
				snapshot, err := browserService.FillVerification(
					ctx, input.Ref, found.Value, expected,
				)
				if err != nil {
					return nil, err
				}
				if err := store.MarkConsumed(id); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{
					"applied": true, "kind": found.Kind,
					"site_domain": expected,
					"page": map[string]string{
						"url": snapshot.URL, "title": snapshot.Title,
					},
				})
			default:
				return nil, errors.New("mailbox: unsupported verification kind")
			}
		},
	})
	return registrations
}

func decodeToolInput(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
