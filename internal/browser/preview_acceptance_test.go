package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

func TestPreviewInspectionBlocksHostileCrossOriginSubresources(t *testing.T) {
	var escapedRequests atomic.Int64
	attacker := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		escapedRequests.Add(1)
		_, _ = writer.Write([]byte(`window.previewEscaped = true`))
	}))
	defer attacker.Close()
	preview := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/missing.png" {
			http.Error(writer, "missing", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(writer, `<!doctype html><title>Owned preview</title>
			<main>isolated preview</main><img src="/missing.png">
			<script>console.error("preview failure")</script>
			<script src="%s/steal.js"></script>`, attacker.URL)
	}))
	defer preview.Close()

	service := openWorkflowBrowser(t)
	defer service.Close()
	sessionID := uuid.New()
	ctx := controlplane.WithApprovalScope(context.Background(), controlplane.ApprovalScope{
		ActorID: uuid.New(), SessionID: &sessionID,
	})
	inspection, err := service.InspectPreview(ctx, preview.URL, 390, 844, true)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Snapshot.Title != "Owned preview" || inspection.ScreenshotPNG == "" ||
		inspection.Width != 390 || inspection.Height != 844 || !inspection.DarkMode {
		t.Fatalf("preview inspection = %+v", inspection)
	}
	if escapedRequests.Load() != 0 {
		t.Fatalf("hostile cross-origin preview made %d external requests", escapedRequests.Load())
	}
	var consoleEvidence, networkEvidence bool
	for _, diagnostic := range inspection.Diagnostics {
		if diagnostic.Source == "console" && len(diagnostic.Evidence) > 0 {
			consoleEvidence = true
		}
		if diagnostic.Source == "network" && diagnostic.Code == "http_404" {
			networkEvidence = true
		}
	}
	if !consoleEvidence || !networkEvidence {
		t.Fatalf("preview diagnostics lack console/source-map or network evidence: %+v", inspection.Diagnostics)
	}
}
