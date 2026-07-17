package reports

import (
	"strings"

	"github.com/xuri/excelize/v2"
)

const excelSheetNameMaxLen = 31

var excelSheetNameDisallowed = strings.NewReplacer(
	"[", "", "]", "", ":", "", "*", "", "?", "", "/", "", "\\", "",
)

// ToExcel renders tabular data as a single-sheet .xlsx file. title becomes
// the sheet name, truncated to Excel's 31-character sheet-name limit and
// stripped of characters Excel disallows in sheet names.
func ToExcel(title string, columns []string, rows [][]string) ([]byte, error) {
	sheet := excelSheetNameDisallowed.Replace(strings.TrimSpace(title))
	if sheet == "" {
		sheet = "Hisobot"
	}
	if len(sheet) > excelSheetNameMaxLen {
		sheet = sheet[:excelSheetNameMaxLen]
	}

	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}

	for col, name := range columns {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			return nil, err
		}
		if err := f.SetCellValue(sheet, cell, name); err != nil {
			return nil, err
		}
	}
	for r, row := range rows {
		for c, value := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				return nil, err
			}
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				return nil, err
			}
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
