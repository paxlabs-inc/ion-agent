package operatorapp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

type browserCredentialHandler struct {
	supervisor    *nativebrowser.Supervisor
	authenticator controlplane.BrowserAuthenticator
	origin        string
}

func (handler browserCredentialHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	actor, err := handler.authenticator.Authenticate(request)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(strings.TrimSpace(request.URL.Query().Get("session_id")))
	if err != nil || sessionID == uuid.Nil {
		http.Error(writer, "valid session_id is required", http.StatusBadRequest)
		return
	}
	ctx := controlplane.WithApprovalScope(request.Context(), controlplane.ApprovalScope{
		ActorID: actor.ActorID, SessionID: &sessionID,
	})
	switch request.Method {
	case http.MethodGet:
		handler.list(writer, ctx)
	case http.MethodPost:
		if request.Header.Get("Origin") != handler.origin ||
			handler.authenticator.ValidateCSRF(request, actor) != nil {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		handler.put(writer, request, ctx)
	default:
		writer.Header().Set("Allow", "GET, POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (handler browserCredentialHandler) list(
	writer http.ResponseWriter,
	ctx context.Context,
) {
	credentials, err := handler.supervisor.Credentials(ctx)
	if err != nil {
		http.Error(writer, "credentials unavailable", http.StatusServiceUnavailable)
		return
	}
	writeBrowserCredentialJSON(writer, http.StatusOK, credentials)
}

func (handler browserCredentialHandler) put(
	writer http.ResponseWriter,
	request *http.Request,
	ctx context.Context,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, 20<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Origin string `json:"origin"`
		Label  string `json:"label"`
		Secret string `json:"secret"`
	}
	if decoder.Decode(&input) != nil {
		http.Error(writer, "invalid credential", http.StatusBadRequest)
		return
	}
	reference, err := handler.supervisor.PutCredential(
		ctx, input.Origin, input.Label, input.Secret,
	)
	input.Secret = ""
	if err != nil {
		http.Error(writer, "credential rejected", http.StatusBadRequest)
		return
	}
	writeBrowserCredentialJSON(writer, http.StatusCreated, reference)
}

func writeBrowserCredentialJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
