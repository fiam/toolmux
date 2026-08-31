package client

import (
	"encoding/json"
	"strconv"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

type sheetsSpreadsheetResult googleapi.Spreadsheet

func (result sheetsSpreadsheetResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.Sheets))
	for _, sheet := range result.Sheets {
		rows = append(rows, []string{
			result.SpreadsheetID,
			result.Properties.Title,
			strconv.Itoa(sheet.Properties.SheetID),
			sheet.Properties.Title,
			strconv.Itoa(sheet.Properties.GridProperties.RowCount),
			strconv.Itoa(sheet.Properties.GridProperties.ColumnCount),
			result.SpreadsheetURL,
		})
	}
	if len(rows) == 0 {
		rows = append(rows, []string{result.SpreadsheetID, result.Properties.Title, "", "", "", "", result.SpreadsheetURL})
	}
	return output.Table{
		Headers: []string{"Spreadsheet ID", "Title", "Sheet ID", "Tab", "Rows", "Columns", "URL"},
		Rows:    rows,
	}
}

type sheetsValuesResult googleapi.BatchGetValuesResponse

func (result sheetsValuesResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.ValueRanges))
	for _, valueRange := range result.ValueRanges {
		rows = append(rows, []string{valueRange.Range, valueRange.MajorDimension, compactSheetsJSON(valueRange.Values)})
	}
	return output.Table{
		Headers: []string{"Range", "Major dimension", "Values"},
		Rows:    rows,
		Empty:   "no values",
	}
}

type sheetsUpdateValuesResult googleapi.UpdateValuesResponse

func (result sheetsUpdateValuesResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Spreadsheet ID", result.SpreadsheetID},
			{"Updated range", result.UpdatedRange},
			{"Updated rows", strconv.Itoa(result.UpdatedRows)},
			{"Updated columns", strconv.Itoa(result.UpdatedColumns)},
			{"Updated cells", strconv.Itoa(result.UpdatedCells)},
		},
	}
}

type sheetsAppendValuesResult googleapi.AppendValuesResponse

func (result sheetsAppendValuesResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Spreadsheet ID", result.SpreadsheetID},
			{"Table range", result.TableRange},
			{"Updated range", result.Updates.UpdatedRange},
			{"Updated rows", strconv.Itoa(result.Updates.UpdatedRows)},
			{"Updated cells", strconv.Itoa(result.Updates.UpdatedCells)},
		},
	}
}

type sheetsBatchUpdateValuesResult googleapi.BatchUpdateValuesResponse

func (result sheetsBatchUpdateValuesResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Spreadsheet ID", result.SpreadsheetID},
			{"Updated sheets", strconv.Itoa(result.TotalUpdatedSheets)},
			{"Updated rows", strconv.Itoa(result.TotalUpdatedRows)},
			{"Updated columns", strconv.Itoa(result.TotalUpdatedColumns)},
			{"Updated cells", strconv.Itoa(result.TotalUpdatedCells)},
			{"Responses", strconv.Itoa(len(result.Responses))},
		},
	}
}

type sheetsClearValuesResult googleapi.BatchClearValuesResponse

func (result sheetsClearValuesResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.ClearedRanges))
	for _, rangeName := range result.ClearedRanges {
		rows = append(rows, []string{result.SpreadsheetID, rangeName})
	}
	return output.Table{
		Headers: []string{"Spreadsheet ID", "Cleared range"},
		Rows:    rows,
		Empty:   "no ranges cleared",
	}
}

type sheetsBatchUpdateResult googleapi.BatchUpdateSpreadsheetResponse

func (result sheetsBatchUpdateResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Spreadsheet ID", result.SpreadsheetID},
			{"Applied requests", strconv.Itoa(result.AppliedRequests)},
			{"Replies", strconv.Itoa(len(result.Replies))},
		},
	}
}

func compactSheetsJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	text := string(data)
	if len(text) <= 800 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= 800 {
		return text
	}
	return string(runes[:800]) + "... truncated ..."
}

var (
	_ actions.TableRenderable = sheetsSpreadsheetResult{}
	_ actions.TableRenderable = sheetsValuesResult{}
	_ actions.TableRenderable = sheetsUpdateValuesResult{}
	_ actions.TableRenderable = sheetsAppendValuesResult{}
	_ actions.TableRenderable = sheetsBatchUpdateValuesResult{}
	_ actions.TableRenderable = sheetsClearValuesResult{}
	_ actions.TableRenderable = sheetsBatchUpdateResult{}
)
