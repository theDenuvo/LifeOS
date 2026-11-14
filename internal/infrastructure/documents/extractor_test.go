package documents

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractDOCX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.docx")
	writeZip(t, path, map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Задача: подготовить отчёт</w:t></w:r></w:p>
<w:p><w:r><w:t>Срок: 20 ноября</w:t></w:r></w:p>
</w:body></w:document>`,
	})

	extraction, err := NewExtractor().Extract(context.Background(), path, "plan.docx", "")
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if !strings.Contains(extraction.Text, "подготовить отчёт") || !strings.Contains(extraction.Text, "20 ноября") {
		t.Fatalf("unexpected docx text: %q", extraction.Text)
	}
}

func TestExtractXLSX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.xlsx")
	writeZip(t, path, map[string]string{
		"xl/styles.xml": `<?xml version="1.0"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><cellXfs count="1"><xf numFmtId="14"/></cellXfs></styleSheet>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<si><t>Задача</t></si><si><t>Позвонить маме</t></si></sst>`,
		"xl/workbook.xml": `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="План" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>завтра</t></is></c><c r="D1" s="0"><v>46389</v></c></row></sheetData></worksheet>`,
	})

	extraction, err := NewExtractor().Extract(context.Background(), path, "tasks.xlsx", "")
	if err != nil {
		t.Fatalf("extract xlsx: %v", err)
	}
	for _, want := range []string{"Лист «План»", "Задача", "Позвонить маме", "завтра", "2027-01-02"} {
		if !strings.Contains(extraction.Text, want) {
			t.Errorf("xlsx text missing %q: %q", want, extraction.Text)
		}
	}
}

func TestExtractWindows1251Text(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	encoded := []byte{0xCf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2} // "Привет" in CP1251
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	extraction, err := NewExtractor().Extract(context.Background(), path, "note.txt", "")
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if extraction.Text != "Привет" {
		t.Fatalf("unexpected decoded text: %q", extraction.Text)
	}
}

func TestExtractXLS(t *testing.T) {
	extraction, err := NewExtractor().Extract(context.Background(), "testdata/sample.xls", "sample.xls", "application/vnd.ms-excel")
	if err != nil {
		t.Fatalf("extract xls: %v", err)
	}
	if len(strings.TrimSpace(extraction.Text)) == 0 {
		t.Fatal("xls extraction returned empty text")
	}
	if !strings.Contains(extraction.Text, "Лист") {
		t.Fatalf("xls text has no sheet marker: %q", extraction.Text[:min(200, len(extraction.Text))])
	}
}
