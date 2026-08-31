package client

import (
	"fmt"
	"strconv"
	"strings"
)

type sheetsA1Part struct {
	kind   string
	column int
	row    int
}

func sheetsGridRange(sheetID int, a1 string) (map[string]any, error) {
	a1 = strings.TrimSpace(a1)
	if a1 == "" {
		return nil, fmt.Errorf("range is required")
	}
	if strings.Contains(a1, "!") {
		return nil, fmt.Errorf("structural Sheets ranges must omit the tab name; pass --sheet-id with an unqualified A1 range")
	}
	parts := strings.Split(a1, ":")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid A1 range %q", a1)
	}
	start, err := parseSheetsA1Part(parts[0])
	if err != nil {
		return nil, err
	}
	end := start
	if len(parts) == 2 {
		end, err = parseSheetsA1Part(parts[1])
		if err != nil {
			return nil, err
		}
		if start.kind != end.kind {
			return nil, fmt.Errorf("A1 range %q must use matching cell, row, or column endpoints", a1)
		}
	}
	grid := map[string]any{"sheetId": sheetID}
	switch start.kind {
	case "cell":
		if end.row < start.row || end.column < start.column {
			return nil, fmt.Errorf("A1 range %q ends before it starts", a1)
		}
		grid["startRowIndex"] = start.row - 1
		grid["endRowIndex"] = end.row
		grid["startColumnIndex"] = start.column - 1
		grid["endColumnIndex"] = end.column
	case "row":
		if end.row < start.row {
			return nil, fmt.Errorf("A1 range %q ends before it starts", a1)
		}
		grid["startRowIndex"] = start.row - 1
		grid["endRowIndex"] = end.row
	case "column":
		if end.column < start.column {
			return nil, fmt.Errorf("A1 range %q ends before it starts", a1)
		}
		grid["startColumnIndex"] = start.column - 1
		grid["endColumnIndex"] = end.column
	}
	return grid, nil
}

func parseSheetsA1Part(value string) (sheetsA1Part, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "$", "")
	if value == "" {
		return sheetsA1Part{}, fmt.Errorf("invalid empty A1 range endpoint")
	}
	lettersEnd := 0
	for lettersEnd < len(value) && isSheetsA1Letter(value[lettersEnd]) {
		lettersEnd++
	}
	letters := strings.ToUpper(value[:lettersEnd])
	digits := value[lettersEnd:]
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return sheetsA1Part{}, fmt.Errorf("invalid A1 range endpoint %q", value)
		}
	}
	switch {
	case letters != "" && digits != "":
		column, err := sheetsColumnNumber(letters)
		if err != nil {
			return sheetsA1Part{}, err
		}
		row, err := positiveA1Number(digits, value)
		if err != nil {
			return sheetsA1Part{}, err
		}
		return sheetsA1Part{kind: "cell", column: column, row: row}, nil
	case letters != "":
		column, err := sheetsColumnNumber(letters)
		return sheetsA1Part{kind: "column", column: column}, err
	case digits != "":
		row, err := positiveA1Number(digits, value)
		return sheetsA1Part{kind: "row", row: row}, err
	default:
		return sheetsA1Part{}, fmt.Errorf("invalid A1 range endpoint %q", value)
	}
}

func isSheetsA1Letter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func sheetsColumnNumber(letters string) (int, error) {
	column := 0
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return 0, fmt.Errorf("invalid A1 column %q", letters)
		}
		column = column*26 + int(letter-'A'+1)
	}
	if column <= 0 {
		return 0, fmt.Errorf("invalid A1 column %q", letters)
	}
	return column, nil
}

func positiveA1Number(value, original string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid A1 range endpoint %q", original)
	}
	return number, nil
}
