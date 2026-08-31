package googleapi

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

const googleSheetsMIME = "application/vnd.google-apps.spreadsheet"

type Spreadsheet struct {
	SpreadsheetID  string                `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	Properties     SpreadsheetProperties `json:"properties,omitzero" yaml:"properties,omitempty"`
	Sheets         []Sheet               `json:"sheets,omitempty" yaml:"sheets,omitempty"`
	SpreadsheetURL string                `json:"spreadsheetUrl,omitempty" yaml:"spreadsheetUrl,omitempty"`
	NamedRanges    []NamedRange          `json:"namedRanges,omitempty" yaml:"namedRanges,omitempty"`
}

type SpreadsheetProperties struct {
	Title    string `json:"title,omitempty" yaml:"title,omitempty"`
	Locale   string `json:"locale,omitempty" yaml:"locale,omitempty"`
	TimeZone string `json:"timeZone,omitempty" yaml:"timeZone,omitempty"`
}

type Sheet struct {
	Properties      SheetProperties  `json:"properties,omitzero" yaml:"properties,omitempty"`
	ProtectedRanges []ProtectedRange `json:"protectedRanges,omitempty" yaml:"protectedRanges,omitempty"`
	Data            []GridData       `json:"data,omitempty" yaml:"data,omitempty"`
}

type SheetProperties struct {
	SheetID        int            `json:"sheetId" yaml:"sheetId"`
	Title          string         `json:"title,omitempty" yaml:"title,omitempty"`
	Index          int            `json:"index,omitempty" yaml:"index,omitempty"`
	SheetType      string         `json:"sheetType,omitempty" yaml:"sheetType,omitempty"`
	GridProperties GridProperties `json:"gridProperties,omitzero" yaml:"gridProperties,omitempty"`
}

type GridProperties struct {
	RowCount          int `json:"rowCount,omitempty" yaml:"rowCount,omitempty"`
	ColumnCount       int `json:"columnCount,omitempty" yaml:"columnCount,omitempty"`
	FrozenRowCount    int `json:"frozenRowCount,omitempty" yaml:"frozenRowCount,omitempty"`
	FrozenColumnCount int `json:"frozenColumnCount,omitempty" yaml:"frozenColumnCount,omitempty"`
}

type ProtectedRange struct {
	ProtectedRangeID int            `json:"protectedRangeId,omitempty" yaml:"protectedRangeId,omitempty"`
	Description      string         `json:"description,omitempty" yaml:"description,omitempty"`
	WarningOnly      bool           `json:"warningOnly,omitempty" yaml:"warningOnly,omitempty"`
	Range            map[string]any `json:"range,omitempty" yaml:"range,omitempty"`
}

type NamedRange struct {
	NamedRangeID string         `json:"namedRangeId,omitempty" yaml:"namedRangeId,omitempty"`
	Name         string         `json:"name,omitempty" yaml:"name,omitempty"`
	Range        map[string]any `json:"range,omitempty" yaml:"range,omitempty"`
}

type GridData struct {
	StartRow    int       `json:"startRow,omitempty" yaml:"startRow,omitempty"`
	StartColumn int       `json:"startColumn,omitempty" yaml:"startColumn,omitempty"`
	RowData     []RowData `json:"rowData,omitempty" yaml:"rowData,omitempty"`
}

type RowData struct {
	Values []map[string]any `json:"values,omitempty" yaml:"values,omitempty"`
}

type ValueRange struct {
	Range          string  `json:"range,omitempty" yaml:"range,omitempty"`
	MajorDimension string  `json:"majorDimension,omitempty" yaml:"majorDimension,omitempty"`
	Values         [][]any `json:"values,omitempty" yaml:"values,omitempty"`
}

type BatchGetValuesResponse struct {
	SpreadsheetID string       `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	ValueRanges   []ValueRange `json:"valueRanges,omitempty" yaml:"valueRanges,omitempty"`
}

type UpdateValuesResponse struct {
	SpreadsheetID  string      `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	UpdatedRange   string      `json:"updatedRange,omitempty" yaml:"updatedRange,omitempty"`
	UpdatedRows    int         `json:"updatedRows,omitempty" yaml:"updatedRows,omitempty"`
	UpdatedColumns int         `json:"updatedColumns,omitempty" yaml:"updatedColumns,omitempty"`
	UpdatedCells   int         `json:"updatedCells,omitempty" yaml:"updatedCells,omitempty"`
	UpdatedData    *ValueRange `json:"updatedData,omitempty" yaml:"updatedData,omitempty"`
}

type AppendValuesResponse struct {
	SpreadsheetID string               `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	TableRange    string               `json:"tableRange,omitempty" yaml:"tableRange,omitempty"`
	Updates       UpdateValuesResponse `json:"updates,omitzero" yaml:"updates,omitempty"`
}

type BatchUpdateValuesResponse struct {
	SpreadsheetID       string                 `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	TotalUpdatedRows    int                    `json:"totalUpdatedRows,omitempty" yaml:"totalUpdatedRows,omitempty"`
	TotalUpdatedColumns int                    `json:"totalUpdatedColumns,omitempty" yaml:"totalUpdatedColumns,omitempty"`
	TotalUpdatedCells   int                    `json:"totalUpdatedCells,omitempty" yaml:"totalUpdatedCells,omitempty"`
	TotalUpdatedSheets  int                    `json:"totalUpdatedSheets,omitempty" yaml:"totalUpdatedSheets,omitempty"`
	Responses           []UpdateValuesResponse `json:"responses,omitempty" yaml:"responses,omitempty"`
}

type BatchClearValuesResponse struct {
	SpreadsheetID string   `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	ClearedRanges []string `json:"clearedRanges,omitempty" yaml:"clearedRanges,omitempty"`
}

type BatchUpdateSpreadsheetResponse struct {
	SpreadsheetID      string           `json:"spreadsheetId,omitempty" yaml:"spreadsheetId,omitempty"`
	Replies            []map[string]any `json:"replies,omitempty" yaml:"replies,omitempty"`
	UpdatedSpreadsheet *Spreadsheet     `json:"updatedSpreadsheet,omitempty" yaml:"updatedSpreadsheet,omitempty"`
	AppliedRequests    int              `json:"applied_requests,omitempty" yaml:"applied_requests,omitempty"`
}

type GetValuesOptions struct {
	MajorDimension       string `json:"majorDimension,omitempty" yaml:"majorDimension,omitempty"`
	ValueRenderOption    string `json:"valueRenderOption,omitempty" yaml:"valueRenderOption,omitempty"`
	DateTimeRenderOption string `json:"dateTimeRenderOption,omitempty" yaml:"dateTimeRenderOption,omitempty"`
}

type WriteValuesOptions struct {
	ValueInputOption             string `json:"valueInputOption,omitempty" yaml:"valueInputOption,omitempty"`
	InsertDataOption             string `json:"insertDataOption,omitempty" yaml:"insertDataOption,omitempty"`
	IncludeValuesInResponse      bool   `json:"includeValuesInResponse,omitempty" yaml:"includeValuesInResponse,omitempty"`
	ResponseValueRenderOption    string `json:"responseValueRenderOption,omitempty" yaml:"responseValueRenderOption,omitempty"`
	ResponseDateTimeRenderOption string `json:"responseDateTimeRenderOption,omitempty" yaml:"responseDateTimeRenderOption,omitempty"`
}

func (c Client) CreateSpreadsheet(ctx context.Context, request map[string]any) (Spreadsheet, error) {
	var out Spreadsheet
	if err := c.postSheetsJSON(ctx, "/v4/spreadsheets", nil, request, &out); err != nil {
		return Spreadsheet{}, err
	}
	return out, nil
}

func (c Client) GetSpreadsheet(ctx context.Context, spreadsheetID string, ranges []string, includeGridData bool) (Spreadsheet, error) {
	values := url.Values{}
	for _, value := range ranges {
		if value = strings.TrimSpace(value); value != "" {
			values.Add("ranges", value)
		}
	}
	if includeGridData {
		values.Set("includeGridData", "true")
	}
	var out Spreadsheet
	if err := c.getSheets(ctx, "/v4/spreadsheets/"+url.PathEscape(strings.TrimSpace(spreadsheetID)), values, &out); err != nil {
		return Spreadsheet{}, err
	}
	return out, nil
}

func (c Client) BatchGetSpreadsheetValues(ctx context.Context, spreadsheetID string, ranges []string, options GetValuesOptions) (BatchGetValuesResponse, error) {
	values := url.Values{}
	for _, value := range ranges {
		if value = strings.TrimSpace(value); value != "" {
			values.Add("ranges", value)
		}
	}
	setIfNotEmpty(values, "majorDimension", options.MajorDimension)
	setIfNotEmpty(values, "valueRenderOption", options.ValueRenderOption)
	setIfNotEmpty(values, "dateTimeRenderOption", options.DateTimeRenderOption)
	var out BatchGetValuesResponse
	if err := c.getSheets(ctx, "/v4/spreadsheets/"+url.PathEscape(strings.TrimSpace(spreadsheetID))+"/values:batchGet", values, &out); err != nil {
		return BatchGetValuesResponse{}, err
	}
	return out, nil
}

func (c Client) UpdateSpreadsheetValues(ctx context.Context, spreadsheetID, rangeName string, valueRange ValueRange, options WriteValuesOptions) (UpdateValuesResponse, error) {
	values := writeValuesQuery(options)
	var out UpdateValuesResponse
	suffix := "/v4/spreadsheets/" + url.PathEscape(strings.TrimSpace(spreadsheetID)) + "/values/" + url.PathEscape(strings.TrimSpace(rangeName))
	if err := c.putSheetsJSON(ctx, suffix, values, valueRange, &out); err != nil {
		return UpdateValuesResponse{}, err
	}
	return out, nil
}

func (c Client) AppendSpreadsheetValues(ctx context.Context, spreadsheetID, rangeName string, valueRange ValueRange, options WriteValuesOptions) (AppendValuesResponse, error) {
	values := writeValuesQuery(options)
	setIfNotEmpty(values, "insertDataOption", options.InsertDataOption)
	var out AppendValuesResponse
	suffix := "/v4/spreadsheets/" + url.PathEscape(strings.TrimSpace(spreadsheetID)) + "/values/" + url.PathEscape(strings.TrimSpace(rangeName)) + ":append"
	if err := c.postSheetsJSON(ctx, suffix, values, valueRange, &out); err != nil {
		return AppendValuesResponse{}, err
	}
	return out, nil
}

func (c Client) BatchUpdateSpreadsheetValues(ctx context.Context, spreadsheetID string, request map[string]any) (BatchUpdateValuesResponse, error) {
	var out BatchUpdateValuesResponse
	suffix := "/v4/spreadsheets/" + url.PathEscape(strings.TrimSpace(spreadsheetID)) + "/values:batchUpdate"
	if err := c.postSheetsJSON(ctx, suffix, nil, request, &out); err != nil {
		return BatchUpdateValuesResponse{}, err
	}
	return out, nil
}

func (c Client) BatchClearSpreadsheetValues(ctx context.Context, spreadsheetID string, ranges []string) (BatchClearValuesResponse, error) {
	var out BatchClearValuesResponse
	suffix := "/v4/spreadsheets/" + url.PathEscape(strings.TrimSpace(spreadsheetID)) + "/values:batchClear"
	if err := c.postSheetsJSON(ctx, suffix, nil, map[string]any{"ranges": ranges}, &out); err != nil {
		return BatchClearValuesResponse{}, err
	}
	return out, nil
}

func (c Client) BatchUpdateSpreadsheetRaw(ctx context.Context, spreadsheetID string, request map[string]any) (BatchUpdateSpreadsheetResponse, error) {
	var out BatchUpdateSpreadsheetResponse
	suffix := "/v4/spreadsheets/" + url.PathEscape(strings.TrimSpace(spreadsheetID)) + ":batchUpdate"
	if err := c.postSheetsJSON(ctx, suffix, nil, request, &out); err != nil {
		return BatchUpdateSpreadsheetResponse{}, err
	}
	out.AppliedRequests = requestCount(request["requests"])
	return out, nil
}

func GoogleSheetsMIMEType() string {
	return googleSheetsMIME
}

func writeValuesQuery(options WriteValuesOptions) url.Values {
	values := url.Values{}
	setIfNotEmpty(values, "valueInputOption", options.ValueInputOption)
	if options.IncludeValuesInResponse {
		values.Set("includeValuesInResponse", strconv.FormatBool(true))
	}
	setIfNotEmpty(values, "responseValueRenderOption", options.ResponseValueRenderOption)
	setIfNotEmpty(values, "responseDateTimeRenderOption", options.ResponseDateTimeRenderOption)
	return values
}

func setIfNotEmpty(values url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values.Set(key, value)
	}
}
