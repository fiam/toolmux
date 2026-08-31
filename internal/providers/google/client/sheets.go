package client

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func handleSheetsCreate(exec actions.Context, inv actions.Invocation) (any, error) {
	request, err := sheetsCreateRequest(inv)
	if err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	spreadsheet, err := client.CreateSpreadsheet(exec.Context, request)
	if err != nil {
		return nil, fmt.Errorf("creating Google spreadsheet failed: %w", err)
	}
	return sheetsSpreadsheetResult(spreadsheet), nil
}

func handleSheetsGet(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	ranges := cleanSheetsRanges(inv.StringSlice("range"))
	includeGridData := inv.Bool("include-grid-data")
	if includeGridData && len(ranges) == 0 {
		return nil, fmt.Errorf("include-grid-data requires at least one --range to bound the response")
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	spreadsheet, err := client.GetSpreadsheet(exec.Context, spreadsheetID, ranges, includeGridData)
	if err != nil {
		return nil, sheetsOperationError("reading", spreadsheetID, err)
	}
	return sheetsSpreadsheetResult(spreadsheet), nil
}

func handleSheetsValuesGet(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	ranges, err := requiredSheetsRanges(inv)
	if err != nil {
		return nil, err
	}
	options, err := sheetsGetValuesOptions(inv)
	if err != nil {
		return nil, err
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchGetSpreadsheetValues(exec.Context, spreadsheetID, ranges, options)
	if err != nil {
		return nil, sheetsOperationError("reading values from", spreadsheetID, err)
	}
	return sheetsValuesResult(response), nil
}

func handleSheetsValuesUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	rangeName, err := requiredString(inv, "range")
	if err != nil {
		return nil, err
	}
	values, err := sheetsValuesInput(exec, inv)
	if err != nil {
		return nil, err
	}
	majorDimension, err := sheetsEnum(inv.String("major-dimension"), "ROWS", "ROWS", "COLUMNS")
	if err != nil {
		return nil, err
	}
	options, err := sheetsWriteValuesOptions(inv, false)
	if err != nil {
		return nil, err
	}
	request := googleapi.ValueRange{Range: rangeName, MajorDimension: majorDimension, Values: values}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, map[string]any{"range": rangeName, "values": request, "options": options}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.UpdateSpreadsheetValues(exec.Context, spreadsheetID, rangeName, request, options)
	if err != nil {
		return nil, sheetsOperationError("updating values in", spreadsheetID, err)
	}
	return sheetsUpdateValuesResult(response), nil
}

func handleSheetsValuesAppend(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	rangeName, err := requiredString(inv, "range")
	if err != nil {
		return nil, err
	}
	values, err := sheetsValuesInput(exec, inv)
	if err != nil {
		return nil, err
	}
	majorDimension, err := sheetsEnum(inv.String("major-dimension"), "ROWS", "ROWS", "COLUMNS")
	if err != nil {
		return nil, err
	}
	options, err := sheetsWriteValuesOptions(inv, true)
	if err != nil {
		return nil, err
	}
	request := googleapi.ValueRange{Range: rangeName, MajorDimension: majorDimension, Values: values}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, map[string]any{"range": rangeName, "values": request, "options": options}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.AppendSpreadsheetValues(exec.Context, spreadsheetID, rangeName, request, options)
	if err != nil {
		return nil, sheetsOperationError("appending values to", spreadsheetID, err)
	}
	return sheetsAppendValuesResult(response), nil
}

func handleSheetsValuesClear(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	ranges, err := requiredSheetsRanges(inv)
	if err != nil {
		return nil, err
	}
	request := map[string]any{"ranges": ranges}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchClearSpreadsheetValues(exec.Context, spreadsheetID, ranges)
	if err != nil {
		return nil, sheetsOperationError("clearing values in", spreadsheetID, err)
	}
	return sheetsClearValuesResult(response), nil
}

func handleSheetsValuesBatchUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	request, err := sheetsRawObject(exec, inv.String("json"), "data")
	if err != nil {
		return nil, err
	}
	if err := requireSheetsRequestArray(request, "data"); err != nil {
		return nil, fmt.Errorf("sheets values batch-update JSON: %w", err)
	}
	if _, ok := request["valueInputOption"]; !ok {
		option, enumErr := sheetsEnum(inv.String("value-input-option"), "RAW", "RAW", "USER_ENTERED")
		if enumErr != nil {
			return nil, enumErr
		}
		request["valueInputOption"] = option
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateSpreadsheetValues(exec.Context, spreadsheetID, request)
	if err != nil {
		return nil, sheetsOperationError("batch-updating values in", spreadsheetID, err)
	}
	return sheetsBatchUpdateValuesResult(response), nil
}

func handleSheetsBatchUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	request, err := sheetsRawObject(exec, inv.String("json"), "requests")
	if err != nil {
		return nil, err
	}
	if err := requireSheetsRequestArray(request, "requests"); err != nil {
		return nil, fmt.Errorf("sheets batch-update JSON: %w", err)
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	return applySheetsBatchUpdate(exec, inv, spreadsheetID, request)
}

func sheetsCreateRequest(inv actions.Invocation) (map[string]any, error) {
	title, err := requiredString(inv, "title")
	if err != nil {
		return nil, err
	}
	properties := map[string]any{"title": title}
	if locale := strings.TrimSpace(inv.String("locale")); locale != "" {
		properties["locale"] = locale
	}
	if timeZone := strings.TrimSpace(inv.String("time-zone")); timeZone != "" {
		properties["timeZone"] = timeZone
	}
	request := map[string]any{"properties": properties}
	sheetTitles, err := sheetsSheetTitles(inv.StringSlice("sheet"))
	if err != nil {
		return nil, err
	}
	rows := inv.Int("rows")
	columns := inv.Int("columns")
	if rows < 0 || columns < 0 {
		return nil, fmt.Errorf("rows and columns cannot be negative")
	}
	if len(sheetTitles) == 0 && (rows > 0 || columns > 0) {
		sheetTitles = []string{"Sheet1"}
	}
	if len(sheetTitles) > 0 {
		sheets := make([]map[string]any, 0, len(sheetTitles))
		for _, sheetTitle := range sheetTitles {
			grid := map[string]any{}
			if rows > 0 {
				grid["rowCount"] = rows
			}
			if columns > 0 {
				grid["columnCount"] = columns
			}
			sheetProperties := map[string]any{"title": sheetTitle}
			if len(grid) > 0 {
				sheetProperties["gridProperties"] = grid
			}
			sheets = append(sheets, map[string]any{"properties": sheetProperties})
		}
		request["sheets"] = sheets
	}
	return request, nil
}

func sheetsSheetTitles(values []string) ([]string, error) {
	seen := map[string]bool{}
	titles := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("sheet titles cannot be empty")
		}
		if seen[value] {
			return nil, fmt.Errorf("duplicate sheet title %q", value)
		}
		seen[value] = true
		titles = append(titles, value)
	}
	return titles, nil
}

func googleSpreadsheetID(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("spreadsheet-id"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	switch {
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the Google spreadsheet as either --spreadsheet-id or a positional argument, not both")
	case flagValue != "":
		return googleDriveFileID(flagValue)
	case argValue != "":
		return googleDriveFileID(argValue)
	default:
		return "", fmt.Errorf("google spreadsheet ID or URL is required")
	}
}

func requiredSheetsRanges(inv actions.Invocation) ([]string, error) {
	ranges := cleanSheetsRanges(inv.StringSlice("range"))
	if len(ranges) == 0 {
		return nil, fmt.Errorf("at least one range is required")
	}
	return ranges, nil
}

func cleanSheetsRanges(values []string) []string {
	ranges := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			ranges = append(ranges, value)
		}
	}
	return ranges
}

func sheetsGetValuesOptions(inv actions.Invocation) (googleapi.GetValuesOptions, error) {
	majorDimension, err := sheetsEnum(inv.String("major-dimension"), "ROWS", "ROWS", "COLUMNS")
	if err != nil {
		return googleapi.GetValuesOptions{}, err
	}
	valueRender, err := sheetsEnum(inv.String("value-render-option"), "FORMATTED_VALUE", "FORMATTED_VALUE", "UNFORMATTED_VALUE", "FORMULA")
	if err != nil {
		return googleapi.GetValuesOptions{}, err
	}
	dateTimeRender, err := sheetsEnum(inv.String("date-time-render-option"), "SERIAL_NUMBER", "SERIAL_NUMBER", "FORMATTED_STRING")
	if err != nil {
		return googleapi.GetValuesOptions{}, err
	}
	return googleapi.GetValuesOptions{
		MajorDimension:       majorDimension,
		ValueRenderOption:    valueRender,
		DateTimeRenderOption: dateTimeRender,
	}, nil
}

func sheetsWriteValuesOptions(inv actions.Invocation, appendValues bool) (googleapi.WriteValuesOptions, error) {
	valueInput, err := sheetsEnum(inv.String("value-input-option"), "RAW", "RAW", "USER_ENTERED")
	if err != nil {
		return googleapi.WriteValuesOptions{}, err
	}
	responseRender, err := sheetsEnum(inv.String("response-value-render-option"), "FORMATTED_VALUE", "FORMATTED_VALUE", "UNFORMATTED_VALUE", "FORMULA")
	if err != nil {
		return googleapi.WriteValuesOptions{}, err
	}
	dateTimeRender, err := sheetsEnum(inv.String("response-date-time-render-option"), "SERIAL_NUMBER", "SERIAL_NUMBER", "FORMATTED_STRING")
	if err != nil {
		return googleapi.WriteValuesOptions{}, err
	}
	options := googleapi.WriteValuesOptions{
		ValueInputOption:             valueInput,
		IncludeValuesInResponse:      inv.Bool("include-values-in-response"),
		ResponseValueRenderOption:    responseRender,
		ResponseDateTimeRenderOption: dateTimeRender,
	}
	if appendValues {
		options.InsertDataOption, err = sheetsEnum(inv.String("insert-data-option"), "INSERT_ROWS", "INSERT_ROWS", "OVERWRITE")
	}
	return options, err
}

func sheetsEnum(value, defaultValue string, allowed ...string) (string, error) {
	value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(firstNonEmpty(value, defaultValue)), "-", "_"))
	if slices.Contains(allowed, value) {
		return value, nil
	}
	return "", fmt.Errorf("unsupported value %q; expected one of %s", value, strings.Join(allowed, ", "))
}

func requireSheetsRequestArray(request map[string]any, name string) error {
	value, ok := request[name]
	if !ok {
		return fmt.Errorf("must include %s", name)
	}
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", name)
	}
	if len(items) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	return nil
}

func applySheetsBatchUpdate(exec actions.Context, inv actions.Invocation, spreadsheetID string, request map[string]any) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateSpreadsheetRaw(exec.Context, spreadsheetID, request)
	if err != nil {
		return nil, sheetsOperationError("updating", spreadsheetID, err)
	}
	return sheetsBatchUpdateResult(response), nil
}

func sheetsOperationError(action, spreadsheetID string, err error) error {
	return fmt.Errorf("%s Google spreadsheet %s failed: %w. If this is an existing spreadsheet, select it first with `toolmux google drive selected add --mime-type %s` so the drive.file grant includes it", action, spreadsheetID, err, googleapi.GoogleSheetsMIMEType())
}
