package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Supported browser-editable extensions.
var supportedExtensions = map[string]bool{
	".docx": true,
	".xlsx": true,
	".pptx": true,
	".pdf":  true,
}

// Macro-enabled extensions that should be quarantined.
var macroExtensions = map[string]bool{
	".docm": true,
	".xlsm": true,
	".pptm": true,
}

// ValidateUploadedFile performs strict validation of an uploaded office file.
func ValidateUploadedFile(filename string, content []byte, maxBytes int64) (string, string, error) {
	if int64(len(content)) > maxBytes {
		return "", "", fmt.Errorf("office: file exceeds maximum size of %d bytes", maxBytes)
	}
	if len(content) == 0 {
		return "", "", fmt.Errorf("office: file is empty")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return "", "", fmt.Errorf("office: file has no extension")
	}

	// Reject macro-enabled formats
	if macroExtensions[ext] {
		return "", "", ErrMacroDetected
	}

	// Check extension support
	if !supportedExtensions[ext] {
		return "", "", fmt.Errorf("office: unsupported file extension %q", ext)
	}

	// MIME sniffing independent of client claims
	detected := http.DetectContentType(content)

	// Validate OOXML ZIP structure for OOXML formats
	if ext == ".docx" || ext == ".xlsx" || ext == ".pptx" {
		if err := validateOOXML(content, ext); err != nil {
			return "", "", err
		}
		// Override MIME for validated OOXML
		switch ext {
		case ".docx":
			detected = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".xlsx":
			detected = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		case ".pptx":
			detected = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		}
	} else {
		if ext == ".pdf" {
			if err := validatePDF(content); err != nil {
				return "", "", err
			}
		}
		// For non-OOXML, verify MIME is reasonable
		if err := validateMIME(ext, detected); err != nil {
			return "", "", err
		}
	}

	return ext, detected, nil
}

func validatePDF(content []byte) error {
	if len(content) < 12 || !bytes.HasPrefix(content, []byte("%PDF-")) {
		return fmt.Errorf("office: invalid PDF header")
	}
	tail := content
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return fmt.Errorf("office: PDF is incomplete")
	}
	for _, marker := range [][]byte{
		[]byte("/JavaScript"),
		[]byte("/JS"),
		[]byte("/Launch"),
		[]byte("/EmbeddedFile"),
		[]byte("/RichMedia"),
	} {
		if bytes.Contains(content, marker) {
			return fmt.Errorf("office: active or embedded PDF content is not supported")
		}
	}
	return nil
}

// validateOOXML validates the ZIP structure of an OOXML file.
func validateOOXML(content []byte, ext string) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fmt.Errorf("office: invalid ZIP structure: %w", err)
	}

	const maxEntries = 500
	const maxDecompressed = 200 << 20 // 200 MiB
	const maxPathLen = 512

	if len(reader.File) > maxEntries {
		return fmt.Errorf("office: ZIP contains too many entries (%d > %d)", len(reader.File), maxEntries)
	}

	var totalDecompressed uint64
	foundContentTypes := false

	requiredMainPart := map[string]string{
		".docx": "word/document.xml",
		".xlsx": "xl/workbook.xml",
		".pptx": "ppt/presentation.xml",
	}
	foundMainPart := false

	for _, f := range reader.File {
		// Check for path traversal
		normalized := strings.ReplaceAll(f.Name, `\`, "/")
		nameForComparison := normalized
		if f.FileInfo().IsDir() {
			nameForComparison = strings.TrimSuffix(normalized, "/")
		}
		cleaned := path.Clean(nameForComparison)
		if normalized != f.Name || cleaned == "." ||
			cleaned != nameForComparison || strings.HasPrefix(cleaned, "/") ||
			strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("office: ZIP contains unsafe path %q", f.Name)
		}
		if len(f.Name) > maxPathLen {
			return fmt.Errorf("office: ZIP entry path too long")
		}
		// Reject symlinks
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("office: ZIP contains a symbolic link")
		}
		if f.FileInfo().IsDir() {
			continue
		}

		totalDecompressed += f.UncompressedSize64
		if totalDecompressed > maxDecompressed {
			return fmt.Errorf("office: decompressed content exceeds maximum")
		}
		lowerName := strings.ToLower(f.Name)
		if strings.HasSuffix(lowerName, "vbaproject.bin") ||
			strings.Contains(lowerName, "/embeddings/") ||
			strings.Contains(lowerName, "/activex/") {
			return ErrMacroDetected
		}
		part, err := f.Open()
		if err != nil {
			return fmt.Errorf("office: open ZIP entry %q: %w", f.Name, err)
		}
		entryLimit := int64(f.UncompressedSize64) + 1
		if entryLimit > maxDecompressed+1 {
			_ = part.Close()
			return fmt.Errorf("office: decompressed content exceeds maximum")
		}
		data, readErr := io.ReadAll(io.LimitReader(part, entryLimit))
		closeErr := part.Close()
		if readErr != nil || closeErr != nil || uint64(len(data)) != f.UncompressedSize64 {
			return fmt.Errorf("office: invalid ZIP entry %q", f.Name)
		}
		if strings.HasSuffix(lowerName, ".rels") {
			if err := validateRelationships(data); err != nil {
				return err
			}
		}

		// Check for [Content_Types].xml
		if f.Name == "[Content_Types].xml" {
			foundContentTypes = true
		}

		// Check main parts
		if f.Name == requiredMainPart[ext] {
			foundMainPart = true
		}
	}

	if !foundContentTypes {
		return fmt.Errorf("office: OOXML missing [Content_Types].xml")
	}
	if !foundMainPart {
		return fmt.Errorf("office: OOXML missing required main part for %s", ext)
	}

	return nil
}

func validateRelationships(content []byte) error {
	var relationships struct {
		Items []struct {
			Type       string `xml:"Type,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(content, &relationships); err != nil {
		return fmt.Errorf("office: invalid OOXML relationships")
	}
	for _, relationship := range relationships.Items {
		if strings.EqualFold(relationship.TargetMode, "External") &&
			!strings.HasSuffix(strings.ToLower(relationship.Type), "/hyperlink") {
			return fmt.Errorf("office: unsafe external OOXML relationship")
		}
	}
	return nil
}

// validateMIME checks that the detected MIME matches the expected type.
func validateMIME(ext, detected string) error {
	expected := map[string][]string{
		".pdf": {"application/pdf"},
	}

	allowed, ok := expected[ext]
	if !ok {
		return nil // Extension already validated as supported
	}
	for _, mime := range allowed {
		if strings.HasPrefix(detected, mime) {
			return nil
		}
	}
	return fmt.Errorf("office: file content does not match extension %s (detected: %s)", ext, detected)
}
