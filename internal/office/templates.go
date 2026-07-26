package office

import (
	_ "embed"

	"github.com/google/uuid"
)

type bundledTemplate struct {
	ID        uuid.UUID
	Kind      DocumentKind
	Name      string
	Extension string
	Filename  string
	Content   []byte
}

var templateNamespace = uuid.MustParse("0cd2650a-0d75-49d4-91fd-5b32f53f5314")

//go:embed template_assets/blank.docx
var blankDocumentContent []byte

//go:embed template_assets/blank.xlsx
var blankSpreadsheetContent []byte

//go:embed template_assets/blank.pptx
var blankPresentationContent []byte

var bundledTemplates = []bundledTemplate{
	newBundledTemplate(
		KindDocument, "Blank Document", ".docx", "blank.docx",
		blankDocumentContent,
	),
	newBundledTemplate(
		KindSpreadsheet, "Blank Spreadsheet", ".xlsx", "blank.xlsx",
		blankSpreadsheetContent,
	),
	newBundledTemplate(
		KindPresentation, "Blank Presentation", ".pptx", "blank.pptx",
		blankPresentationContent,
	),
}

func newBundledTemplate(
	kind DocumentKind,
	name string,
	extension string,
	filename string,
	content []byte,
) bundledTemplate {
	return bundledTemplate{
		ID:        uuid.NewSHA1(templateNamespace, []byte(filename)),
		Kind:      kind,
		Name:      name,
		Extension: extension,
		Filename:  filename,
		Content:   content,
	}
}
