package documents

import (
	"fmt"
	"strconv"
	"strings"

	xlsreader "github.com/shakinm/xlsReader/xls"
	"github.com/shakinm/xlsReader/xls/structure"
)

// extractXLS reads legacy BIFF .xls workbooks. Dates are detected through the
// cell XF/format index because the underlying cell value is an Excel serial.
func extractXLS(path string) (string, error) {
	workbook, err := xlsreader.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("xls: %w", err)
	}

	var builder strings.Builder
	const maxRows = 20000
	for index := 0; index < workbook.GetNumberSheets(); index++ {
		sheet, err := workbook.GetSheet(index)
		if err != nil {
			return "", fmt.Errorf("xls sheet %d: %w", index, err)
		}
		if sheet == nil {
			continue
		}
		fmt.Fprintf(&builder, "Лист «%s»\n", sheet.GetName())
		rows := sheet.GetRows()
		if len(rows) > maxRows {
			rows = rows[:maxRows]
		}
		for _, row := range rows {
			columns := row.GetCols()
			cells := make([]string, len(columns))
			for i, cell := range columns {
				cells[i] = xlsCellText(&workbook, cell)
			}
			builder.WriteString(strings.Join(cells, "\t"))
			builder.WriteByte('\n')
			if builder.Len() > maxTextBytes {
				return cleanExtractedText(builder.String()), nil
			}
		}
		builder.WriteString("\n")
	}
	return cleanExtractedText(builder.String()), nil
}

func xlsCellText(workbook *xlsreader.Workbook, cell structure.CellData) string {
	if cell == nil {
		return ""
	}
	typeName := cell.GetType()
	isNumeric := strings.Contains(typeName, "Number") || strings.Contains(typeName, "Rk")
	if isNumeric {
		xf := workbook.GetXFbyIndex(cell.GetXFIndex())
		formatIndex := xf.GetFormatIndex()
		if isXLDateStyle(formatIndex) {
			return excelDateText(strconv.FormatFloat(cell.GetFloat64(), 'f', -1, 64))
		}
		if formatIndex >= 164 {
			format := workbook.GetFormatByIndex(formatIndex)
			if format.GetIndex() != 0 && isXLDateFormat(format.String()) {
				return excelDateText(strconv.FormatFloat(cell.GetFloat64(), 'f', -1, 64))
			}
		}
	}
	return cell.GetString()
}

func isXLDateStyle(index int) bool {
	return (index >= 14 && index <= 22) || (index >= 27 && index <= 36) ||
		(index >= 45 && index <= 47) || (index >= 50 && index <= 58)
}

func isXLDateFormat(format string) bool {
	lower := strings.ToLower(format)
	return strings.Contains(lower, "yy") || strings.Contains(lower, "dd") ||
		strings.Contains(lower, "mm") || strings.Contains(lower, "hh") || strings.Contains(lower, "ss")
}
