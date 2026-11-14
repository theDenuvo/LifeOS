package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	"golang.org/x/text/encoding/charmap"
)

const (
	maxTextBytes  = 2 << 20
	maxImageBytes = 12 << 20
	maxPDFBytes   = 8 << 20
)

var (
	tagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
	scriptPattern = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)

// Extractor reads text or image data from local files.
type Extractor struct{}

// NewExtractor builds a local extractor.
func NewExtractor() *Extractor { return &Extractor{} }

// Extract dispatches by extension and content. Images are returned as bytes for
// the vision model; everything else is converted to text locally.
func (e *Extractor) Extract(ctx context.Context, path, filename, mime string) (appdocuments.Extraction, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if mime == "" {
		mime = mimeFromExt(ext)
	}
	if isImage(ext, mime) {
		data, err := readLimited(path, maxImageBytes)
		if err != nil {
			return appdocuments.Extraction{}, err
		}
		detected := http.DetectContentType(data)
		if !strings.HasPrefix(detected, "image/") {
			return appdocuments.Extraction{}, fmt.Errorf("файл %s не является изображением", filename)
		}
		mime = detected
		return appdocuments.Extraction{Image: data, ImageMIME: mime}, nil
	}

	switch ext {
	case ".docx":
		text, err := extractDOCX(path)
		return appdocuments.Extraction{Text: text}, err
	case ".pptx":
		text, err := extractPPTX(path)
		return appdocuments.Extraction{Text: text}, err
	case ".xlsx":
		text, err := extractXLSX(path)
		return appdocuments.Extraction{Text: text}, err
	case ".xls":
		text, err := extractXLS(path)
		return appdocuments.Extraction{Text: text}, err
	case ".pdf":
		text, err := extractPDF(ctx, path)
		return appdocuments.Extraction{Text: text}, err
	case ".doc", ".ppt":
		return appdocuments.Extraction{}, fmt.Errorf("старый формат %s не поддерживается; сохраните файл как %sx", ext, ext)
	case ".html", ".htm":
		data, err := readLimited(path, maxTextBytes)
		if err != nil {
			return appdocuments.Extraction{}, err
		}
		return appdocuments.Extraction{Text: stripHTML(decodeText(data))}, nil
	default:
		if isTextExt(ext) || strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/xml" {
			data, err := readLimited(path, maxTextBytes)
			if err != nil {
				return appdocuments.Extraction{}, err
			}
			return appdocuments.Extraction{Text: decodeText(data)}, nil
		}
		return appdocuments.Extraction{}, fmt.Errorf("тип файла %s не поддерживается", ext)
	}
}

func isImage(ext, mime string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	}
	return strings.HasPrefix(mime, "image/")
}

func isTextExt(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".xml", ".yaml", ".yml", ".log", ".ini", ".conf", ".toml":
		return true
	default:
		return false
	}
}

func mimeFromExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	default:
		return "text/plain"
	}
}

func readLimited(path string, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("файл слишком большой для обработки")
	}
	return data, nil
}

// decodeText accepts UTF-8, UTF-16 with BOM, and falls back to Windows-1251,
// which is common for Russian text files produced on Windows.
func decodeText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return decodeUTF16(data[2:], true)
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeUTF16(data[2:], false)
		}
	}
	if utf8.Valid(data) {
		return strings.TrimRight(string(data), "\x00")
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err == nil && utf8.Valid(decoded) {
		return strings.TrimRight(string(decoded), "\x00")
	}
	return strings.ToValidUTF8(string(data), "")
}

func decodeUTF16(data []byte, littleEndian bool) string {
	words := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if littleEndian {
			words = append(words, uint16(data[i])|uint16(data[i+1])<<8)
		} else {
			words = append(words, uint16(data[i])<<8|uint16(data[i+1]))
		}
	}
	return string(utf16.Decode(words))
}

func stripHTML(value string) string {
	value = scriptPattern.ReplaceAllString(value, " ")
	value = tagPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(html.UnescapeString(value)), " ")
}

func extractDOCX(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("docx: %w", err)
	}
	defer archive.Close()

	var body io.ReadCloser
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			body, err = file.Open()
			if err != nil {
				return "", err
			}
			break
		}
	}
	if body == nil {
		return "", errors.New("docx: word/document.xml not found")
	}
	defer body.Close()

	decoder := xml.NewDecoder(io.LimitReader(body, maxTextBytes))
	var builder strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("docx: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(value.Name.Local) {
			case "t":
				inText = true
			case "tab":
				builder.WriteByte('\t')
			case "br":
				builder.WriteByte('\n')
			}
		case xml.EndElement:
			switch strings.ToLower(value.Name.Local) {
			case "t":
				inText = false
			case "p":
				builder.WriteByte('\n')
			case "tc":
				builder.WriteByte('\t')
			case "tr":
				builder.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				builder.Write(value)
			}
		}
	}
	return cleanExtractedText(builder.String()), nil
}

func extractPPTX(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("pptx: %w", err)
	}
	defer archive.Close()

	slides := make([]*zip.File, 0)
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slides = append(slides, file)
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].Name < slides[j].Name })

	var builder strings.Builder
	for _, slide := range slides {
		reader, err := slide.Open()
		if err != nil {
			return "", err
		}
		decoder := xml.NewDecoder(io.LimitReader(reader, maxTextBytes))
		inText := false
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				_ = reader.Close()
				return "", fmt.Errorf("pptx: %w", err)
			}
			switch value := token.(type) {
			case xml.StartElement:
				if strings.ToLower(value.Name.Local) == "t" {
					inText = true
				}
			case xml.EndElement:
				if strings.ToLower(value.Name.Local) == "t" {
					inText = false
				}
			case xml.CharData:
				if inText {
					builder.Write(value)
				}
			}
		}
		_ = reader.Close()
		builder.WriteString("\n\n")
	}
	return cleanExtractedText(builder.String()), nil
}

func extractXLSX(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("xlsx: %w", err)
	}
	defer archive.Close()

	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		files[file.Name] = file
	}
	shared, err := readSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return "", err
	}
	dateStyles := readDateStyles(files["xl/styles.xml"])
	sheets := workbookSheets(files)
	if len(sheets) == 0 {
		for name := range files {
			if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
				sheets = append(sheets, sheetRef{Name: name, Path: name})
			}
		}
		sort.Slice(sheets, func(i, j int) bool { return sheets[i].Path < sheets[j].Path })
	}

	var builder strings.Builder
	for _, sheet := range sheets {
		file := files[sheet.Path]
		if file == nil {
			continue
		}
		if sheet.Name != "" {
			fmt.Fprintf(&builder, "Лист «%s»\n", sheet.Name)
		}
		if err := appendSheetRows(&builder, file, shared, dateStyles); err != nil {
			return "", err
		}
		builder.WriteString("\n")
	}
	return cleanExtractedText(builder.String()), nil
}

type sheetRef struct {
	Name string
	Path string
}

func readSharedStrings(file *zip.File) ([]string, error) {
	if file == nil {
		return nil, nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	decoder := xml.NewDecoder(io.LimitReader(reader, maxTextBytes))
	values := make([]string, 0)
	var current strings.Builder
	inText := false
	inItem := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xlsx shared strings: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(value.Name.Local) {
			case "si":
				inItem = true
				current.Reset()
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch strings.ToLower(value.Name.Local) {
			case "si":
				if inItem {
					values = append(values, current.String())
					inItem = false
				}
			case "t":
				inText = false
			}
		case xml.CharData:
			if inItem && inText {
				current.Write(value)
			}
		}
	}
	return values, nil
}

func workbookSheets(files map[string]*zip.File) []sheetRef {
	workbook := files["xl/workbook.xml"]
	if workbook == nil {
		return nil
	}
	reader, err := workbook.Open()
	if err != nil {
		return nil
	}
	defer reader.Close()

	type sheet struct {
		Name string
		RID  string
	}
	decoder := xml.NewDecoder(io.LimitReader(reader, maxTextBytes))
	list := make([]sheet, 0)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil
		}
		start, ok := token.(xml.StartElement)
		if !ok || strings.ToLower(start.Name.Local) != "sheet" {
			continue
		}
		value := sheet{}
		for _, attr := range start.Attr {
			switch strings.ToLower(attr.Name.Local) {
			case "name":
				value.Name = attr.Value
			case "id":
				value.RID = attr.Value
			}
		}
		list = append(list, value)
	}

	rels := map[string]string{}
	if relFile := files["xl/_rels/workbook.xml.rels"]; relFile != nil {
		relReader, err := relFile.Open()
		if err == nil {
			relDecoder := xml.NewDecoder(io.LimitReader(relReader, maxTextBytes))
			for {
				token, err := relDecoder.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					break
				}
				start, ok := token.(xml.StartElement)
				if !ok || strings.ToLower(start.Name.Local) != "relationship" {
					continue
				}
				values := map[string]string{}
				for _, attr := range start.Attr {
					values[strings.ToLower(attr.Name.Local)] = attr.Value
				}
				if values["id"] != "" && values["target"] != "" {
					target := values["target"]
					if strings.HasPrefix(target, "/") {
						target = strings.TrimPrefix(target, "/")
					} else if !strings.HasPrefix(target, "xl/") {
						target = "xl/" + target
					}
					rels[values["id"]] = target
				}
			}
			_ = relReader.Close()
		}
	}

	result := make([]sheetRef, 0, len(list))
	for _, item := range list {
		path := rels[item.RID]
		if path == "" {
			continue
		}
		result = append(result, sheetRef{Name: item.Name, Path: path})
	}
	return result
}

func appendSheetRows(builder *strings.Builder, file *zip.File, shared []string, dateStyles map[int]bool) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()

	decoder := xml.NewDecoder(io.LimitReader(reader, maxTextBytes))
	row := map[int]string{}
	maxColumn := 0
	inValue := false
	inInline := false
	cellType := ""
	cellColumn := 0
	cellStyle := 0
	var value strings.Builder

	flushRow := func() {
		if maxColumn == 0 {
			return
		}
		columns := make([]string, maxColumn)
		for index, text := range row {
			if index >= 0 && index < len(columns) {
				columns[index] = text
			}
		}
		builder.WriteString(strings.Join(columns, "\t"))
		builder.WriteByte('\n')
		row = map[int]string{}
		maxColumn = 0
	}

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("xlsx sheet: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(element.Name.Local) {
			case "row":
				row = map[int]string{}
				maxColumn = 0
			case "c":
				cellType = ""
				cellColumn = 0
				cellStyle = 0
				value.Reset()
				for _, attr := range element.Attr {
					switch strings.ToLower(attr.Name.Local) {
					case "r":
						cellColumn = columnIndex(attr.Value)
					case "t":
						cellType = attr.Value
					case "s":
						if parsed, err := strconv.Atoi(attr.Value); err == nil {
							cellStyle = parsed
						}
					}
				}
			case "v":
				inValue = true
			case "is":
				inInline = true
			case "t":
				if inInline {
					inValue = true
				}
			}
		case xml.EndElement:
			switch strings.ToLower(element.Name.Local) {
			case "v":
				inValue = false
			case "c":
				text := value.String()
				if cellType == "s" {
					if index, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && index >= 0 && index < len(shared) {
						text = shared[index]
					}
				} else if (cellType == "" || cellType == "n") && dateStyles[cellStyle] {
					text = excelDateText(text)
				} else if cellType == "d" {
					if parsed, err := time.Parse(time.RFC3339, text); err == nil {
						text = parsed.Format("2006-01-02")
					}
				}
				if cellColumn > 0 {
					row[cellColumn-1] = text
					if cellColumn > maxColumn {
						maxColumn = cellColumn
					}
				}
			case "is":
				inInline = false
			case "t":
				if inInline {
					inValue = false
				}
			case "row":
				flushRow()
			}
		case xml.CharData:
			if inValue {
				value.Write(element)
			}
		}
	}
	flushRow()
	return nil
}

// columnIndex converts A1, B2, AA10 to a 1-based column number.
func columnIndex(reference string) int {
	column := 0
	for _, r := range reference {
		if r < 'A' || r > 'Z' {
			break
		}
		column = column*26 + int(r-'A'+1)
	}
	return column
}

func readDateStyles(file *zip.File) map[int]bool {
	if file == nil {
		return nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil
	}
	defer reader.Close()

	custom := map[int]string{}
	styles := map[int]bool{}
	inCellXfs := false
	styleIndex := 0
	decoder := xml.NewDecoder(io.LimitReader(reader, maxTextBytes))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return styles
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(value.Name.Local) {
			case "numfmt":
				id := 0
				format := ""
				for _, attr := range value.Attr {
					switch strings.ToLower(attr.Name.Local) {
					case "numfmtid":
						id, _ = strconv.Atoi(attr.Value)
					case "formatcode":
						format = attr.Value
					}
				}
				if id > 0 {
					custom[id] = format
				}
			case "cellxfs":
				inCellXfs = true
			case "xf":
				if inCellXfs {
					id := 0
					for _, attr := range value.Attr {
						if strings.ToLower(attr.Name.Local) == "numfmtid" {
							id, _ = strconv.Atoi(attr.Value)
						}
					}
					styles[styleIndex] = isDateFormat(id, custom[id])
					styleIndex++
				}
			}
		case xml.EndElement:
			if strings.ToLower(value.Name.Local) == "cellxfs" {
				inCellXfs = false
			}
		}
	}
	return styles
}

func isDateFormat(id int, format string) bool {
	if (id >= 14 && id <= 22) || (id >= 27 && id <= 36) || (id >= 45 && id <= 47) || (id >= 50 && id <= 58) {
		return true
	}
	if format == "" {
		return false
	}
	lower := strings.ToLower(format)
	return strings.Contains(lower, "yy") || strings.Contains(lower, "dd") || strings.Contains(lower, "mm") || strings.Contains(lower, "hh") || strings.Contains(lower, "ss")
}

func excelDateText(value string) string {
	serial, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || serial <= 0 || serial > 2958465 {
		return value
	}
	days := int(serial)
	fraction := serial - float64(days)
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	date := base.AddDate(0, 0, days)
	if fraction > 0 {
		minutes := int(fraction*24*60 + 0.5)
		return fmt.Sprintf("%s %02d:%02d", date.Format("2006-01-02"), minutes/60, minutes%60)
	}
	return date.Format("2006-01-02")
}

func extractPDF(ctx context.Context, path string) (string, error) {
	binary, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("для PDF нужен pdftotext (poppler); на этом компьютере он не найден")
	}
	command := exec.CommandContext(ctx, binary, "-layout", "-enc", "UTF-8", path, "-")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}
	if len(output) > maxPDFBytes {
		output = output[:maxPDFBytes]
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "", errors.New("в PDF нет текстового слоя; загрузите страницы как изображения")
	}
	return cleanExtractedText(text), nil
}

func cleanExtractedText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\x00", "")
	lines := strings.Split(value, "\n")
	clean := make([]string, 0, len(lines))
	empty := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			empty++
			if empty > 2 {
				continue
			}
		} else {
			empty = 0
		}
		clean = append(clean, line)
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}
