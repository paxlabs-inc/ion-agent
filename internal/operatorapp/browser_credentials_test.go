package operatorapp

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type credentialTestAuthenticator struct {
	actorID uuid.UUID
}

func (auth credentialTestAuthenticator) Authenticate(
	*http.Request,
) (controlplane.BrowserActor, error) {
	return controlplane.BrowserActor{ActorID: auth.actorID}, nil
}

func (credentialTestAuthenticator) ValidateCSRF(
	request *http.Request,
	_ controlplane.BrowserActor,
) error {
	if request.Header.Get(controlplane.CSRFHeaderName) != "valid" {
		return errors.New("invalid csrf")
	}
	return nil
}

func TestBrowserCredentialHandlerIsWriteOnlyOriginAndSessionBound(t *testing.T) {
	executable := ""
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if found, err := exec.LookPath(name); err == nil {
			executable = found
			break
		}
	}
	if executable == "" {
		t.Skip("Chromium unavailable")
	}
	browser, err := nativebrowser.New(nativebrowser.Config{
		ExecutablePath: executable, ProfileRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	cipher, err := vault.New(bytes.Repeat([]byte{0x73}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	statePath := filepath.Join(t.TempDir(), "workflows.enc")
	supervisor, err := nativebrowser.OpenSupervisor(
		browser, statePath, cipher, types.SystemClock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	actorID, sessionID := uuid.New(), uuid.New()
	handler := browserCredentialHandler{
		supervisor:    supervisor,
		authenticator: credentialTestAuthenticator{actorID: actorID},
		origin:        "https://ion.test",
	}
	target := "/v1/browser-credentials?session_id=" + url.QueryEscape(sessionID.String())
	body := `{"origin":"https://service.test","label":"Primary","secret":"never-return-this"}`
	forbidden := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	forbidden.Header.Set("Origin", "https://other.test")
	forbidden.Header.Set(controlplane.CSRFHeaderName, "valid")
	forbiddenResult := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResult, forbidden)
	if forbiddenResult.Code != http.StatusForbidden {
		t.Fatalf("wrong-origin status = %d", forbiddenResult.Code)
	}
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.Header.Set("Origin", "https://ion.test")
	request.Header.Set(controlplane.CSRFHeaderName, "valid")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusCreated ||
		strings.Contains(result.Body.String(), "never-return-this") ||
		!strings.Contains(result.Body.String(), `"origin":"https://service.test"`) {
		t.Fatalf("write-only response = %d %s", result.Code, result.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, target, nil)
	listResult := httptest.NewRecorder()
	handler.ServeHTTP(listResult, list)
	if listResult.Code != http.StatusOK ||
		strings.Contains(listResult.Body.String(), "never-return-this") ||
		!strings.Contains(listResult.Body.String(), `"label":"Primary"`) {
		t.Fatalf("credential list = %d %s", listResult.Code, listResult.Body.String())
	}
}
