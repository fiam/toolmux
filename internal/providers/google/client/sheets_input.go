package client

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
)

func sheetsValuesInput(exec actions.Context, inv actions.Invocation) ([][]any, error) {
	rawJSON := strings.TrimSpace(inv.String("values-json"))
	filePath := strings.TrimSpace(inv.String("file"))
	if (rawJSON == "") == (filePath == "") {
		return nil, fmt.Errorf("pass exactly one of --values-json or --file")
	}
	if rawJSON != "" {
		raw, err := sheetsJSONInput(exec, rawJSON)
		if err != nil {
			return nil, err
		}
		return decodeSheetsValuesJSON(raw)
	}
	readFile := exec.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	format, err := sheetsInputFormat(inv.String("input-format"), filePath)
	if err != nil {
		return nil, err
	}
	if format == "json" {
		return decodeSheetsValuesJSON(string(data))
	}
	return decodeSheetsDelimitedValues(string(data), format)
}

func sheetsJSONInput(exec actions.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("json is required")
	}
	path, fromFile := strings.CutPrefix(raw, "@")
	if !fromFile {
		return raw, nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("json file path is required after @")
	}
	readFile := exec.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeSheetsValuesJSON(raw string) ([][]any, error) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Sheets values JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("sheets values JSON must contain one value")
	}
	rows, ok := decoded.([]any)
	if !ok {
		return nil, fmt.Errorf("sheets values JSON must be a two-dimensional array")
	}
	values := make([][]any, 0, len(rows))
	for rowIndex, rawRow := range rows {
		row, ok := rawRow.([]any)
		if !ok {
			return nil, fmt.Errorf("sheets values JSON row %d must be an array", rowIndex+1)
		}
		for columnIndex, value := range row {
			if !validSheetsCellValue(value) {
				return nil, fmt.Errorf("sheets values JSON cell %d,%d must be a string, number, boolean, or null", rowIndex+1, columnIndex+1)
			}
		}
		values = append(values, row)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("sheets values must contain at least one row")
	}
	return values, nil
}

func validSheetsCellValue(value any) bool {
	switch value.(type) {
	case nil, string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

func sheetsInputFormat(value, filePath string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(firstNonEmpty(value, "auto")))
	if value == "auto" {
		switch strings.ToLower(filepath.Ext(filePath)) {
		case ".json":
			value = "json"
		case ".csv":
			value = "csv"
		case ".tsv", ".tab":
			value = "tsv"
		default:
			return "", fmt.Errorf("could not infer input format from %q; pass --input-format json, csv, or tsv", filePath)
		}
	}
	switch value {
	case "json", "csv", "tsv":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported Sheets input format %q", value)
	}
}

func decodeSheetsDelimitedValues(raw, format string) ([][]any, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	if format == "tsv" {
		reader.Comma = '\t'
	}
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode Sheets %s input: %w", format, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("sheets %s input must contain at least one row", format)
	}
	values := make([][]any, 0, len(records))
	for _, record := range records {
		row := make([]any, len(record))
		for i, value := range record {
			row[i] = value
		}
		values = append(values, row)
	}
	return values, nil
}

func sheetsRawObject(exec actions.Context, raw, collectionName string) (map[string]any, error) {
	input, err := sheetsJSONInput(exec, raw)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Sheets JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("sheets JSON must contain one value")
	}
	switch value := decoded.(type) {
	case map[string]any:
		return value, nil
	case []any:
		return map[string]any{collectionName: value}, nil
	default:
		return nil, fmt.Errorf("sheets JSON must be an object or %s array", collectionName)
	}
}
