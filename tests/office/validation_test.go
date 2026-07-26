package office_test

import (
	"archive/zip"
	"bytes"
	"testing"

	office "github.com/paxlabs-inc/ion-agent/internal/office"
)

func TestValidateUploadedFile_Extension(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{"valid docx", "test.docx", false},
		{"valid xlsx", "test.xlsx", false},
		{"valid pptx", "test.pptx", false},
		{"valid pdf", "test.pdf", false},
		{"unsupported csv", "data.csv", true},
		{"unsupported txt", "notes.txt", true},
		{"macro docm", "macro.docm", true},
		{"macro xlsm", "macro.xlsm", true},
		{"macro pptm", "macro.pptm", true},
		{"no extension", "noext", true},
		{"unsupported", "file.xyz", true},
		{"executable", "file.exe", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := make([]byte, 100)
			if tt.filename == "notes.txt" || tt.filename == "data.csv" {
				for i := range content {
					content[i] = 'a'
				}
			} else if tt.filename == "test.pdf" {
				content = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF")
			} else if tt.filename == "noext" || tt.filename == "file.xyz" || tt.filename == "file.exe" {
				content = []byte("some content")
			} else if !tt.wantErr {
				content = makeValidOOXML(t, tt.filename)
			} else {
				content = []byte("bad content")
			}
			_, _, err := office.ValidateUploadedFile(tt.filename, content, 10<<20)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUploadedFile(%s) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			}
		})
	}
}

func TestValidateUploadedFile_EmptyFile(t *testing.T) {
	_, _, err := office.ValidateUploadedFile("test.docx", nil, 10<<20)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestValidateUploadedFile_TooLarge(t *testing.T) {
	content := make([]byte, 101)
	_, _, err := office.ValidateUploadedFile("test.txt", content, 100)
	if err == nil {
		t.Error("expected error for oversized file")
	}
}

func makeValidOOXML(t *testing.T, filename string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add [Content_Types].xml
	ct, err := w.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`))

	// Add main part based on extension
	var mainPart string
	switch {
	case bytes.HasSuffix([]byte(filename), []byte(".docx")):
		mainPart = "word/document.xml"
	case bytes.HasSuffix([]byte(filename), []byte(".xlsx")):
		mainPart = "xl/workbook.xml"
	case bytes.HasSuffix([]byte(filename), []byte(".pptx")):
		mainPart = "ppt/presentation.xml"
	}
	if mainPart != "" {
		mp, err := w.Create(mainPart)
		if err != nil {
			t.Fatal(err)
		}
		mp.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><root/>`))
	}

	w.Close()
	return buf.Bytes()
}

func TestValidateUploadedFile_ZIPTraversal(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../../etc/passwd")
	f.Write([]byte("malicious"))
	w.Close()

	_, _, err := office.ValidateUploadedFile("test.docx", buf.Bytes(), 10<<20)
	if err == nil {
		t.Error("expected error for ZIP path traversal")
	}
}
