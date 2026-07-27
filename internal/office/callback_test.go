package office

import (
	"bytes"
	"encoding/json"
	"net/url"
	"testing"
)

func TestInternalEngineDownloadURLRewritesPublicProxyOrigin(t *testing.T) {
	publicOrigin, err := url.Parse("https://ion.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		engine:       &OnlyOfficeEngine{internalURL: "http://127.0.0.1:80"},
		publicPath:   "/office-engine/",
		publicOrigin: publicOrigin,
	}
	actual, err := service.internalEngineDownloadURL(
		"https://ion.example/office-engine/cache/files/output.docx?token=signed",
	)
	if err != nil {
		t.Fatal(err)
	}
	expected := "http://127.0.0.1:80/cache/files/output.docx?token=signed"
	if actual != expected {
		t.Fatalf("internalEngineDownloadURL() = %q, want %q", actual, expected)
	}
}

func TestInternalEngineDownloadURLLeavesInternalOriginUntouched(t *testing.T) {
	publicOrigin, err := url.Parse("https://ion.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		engine:       &OnlyOfficeEngine{internalURL: "http://127.0.0.1:80"},
		publicPath:   "/office-engine/",
		publicOrigin: publicOrigin,
	}
	const internal = "http://127.0.0.1:80/cache/files/output.docx"
	actual, err := service.internalEngineDownloadURL(internal)
	if err != nil {
		t.Fatal(err)
	}
	if actual != internal {
		t.Fatalf("internalEngineDownloadURL() = %q, want %q", actual, internal)
	}
}

func TestCallbackRequestAcceptsOnlyOfficeSaveFields(t *testing.T) {
	payload := []byte(`{
		"key":"document-key",
		"status":2,
		"url":"https://ion.example/office-engine/cache/output.docx",
		"lastsave":"2026-07-27T05:07:33.000Z",
		"notmodified":false
	}`)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request CallbackRequest
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode ONLYOFFICE callback: %v", err)
	}
	if request.LastSave == "" || request.NotModified {
		t.Fatalf("decoded callback = %+v", request)
	}
}
