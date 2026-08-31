package client

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
)

func handleSheetsTabsAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	title, err := requiredString(inv, "title")
	if err != nil {
		return nil, err
	}
	properties := map[string]any{"title": title}
	grid := map[string]any{}
	if rows := inv.Int("rows"); rows < 0 {
		return nil, fmt.Errorf("rows cannot be negative")
	} else if rows > 0 {
		grid["rowCount"] = rows
	}
	if columns := inv.Int("columns"); columns < 0 {
		return nil, fmt.Errorf("columns cannot be negative")
	} else if columns > 0 {
		grid["columnCount"] = columns
	}
	if len(grid) > 0 {
		properties["gridProperties"] = grid
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{"addSheet": map[string]any{"properties": properties}})
}

func handleSheetsTabsDelete(exec actions.Context, inv actions.Invocation) (any, error) {
	sheetID, err := requiredSheetID(inv)
	if err != nil {
		return nil, err
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{"deleteSheet": map[string]any{"sheetId": sheetID}})
}

func handleSheetsTabsRename(exec actions.Context, inv actions.Invocation) (any, error) {
	sheetID, err := requiredSheetID(inv)
	if err != nil {
		return nil, err
	}
	title, err := requiredString(inv, "title")
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"updateSheetProperties": map[string]any{
			"properties": map[string]any{"sheetId": sheetID, "title": title},
			"fields":     "title",
		},
	}
	return applyDedicatedSheetsRequest(exec, inv, request)
}

func handleSheetsRowsInsert(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleSheetsDimensionChange(exec, inv, "ROWS", true)
}

func handleSheetsRowsDelete(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleSheetsDimensionChange(exec, inv, "ROWS", false)
}

func handleSheetsColumnsInsert(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleSheetsDimensionChange(exec, inv, "COLUMNS", true)
}

func handleSheetsColumnsDelete(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleSheetsDimensionChange(exec, inv, "COLUMNS", false)
}

func handleSheetsDimensionChange(exec actions.Context, inv actions.Invocation, dimension string, insert bool) (any, error) {
	sheetID, err := requiredSheetID(inv)
	if err != nil {
		return nil, err
	}
	start := inv.Int("start")
	count := inv.Int("count")
	if start <= 0 {
		return nil, fmt.Errorf("start must be a positive one-based index")
	}
	if count <= 0 {
		return nil, fmt.Errorf("count must be greater than zero")
	}
	rangeValue := map[string]any{
		"sheetId":    sheetID,
		"dimension":  dimension,
		"startIndex": start - 1,
		"endIndex":   start - 1 + count,
	}
	requestName := "deleteDimension"
	body := map[string]any{"range": rangeValue}
	if insert {
		requestName = "insertDimension"
		inheritFromBefore := inv.Bool("inherit-from-before")
		if start == 1 && inheritFromBefore {
			return nil, fmt.Errorf("inherit-from-before cannot be used when inserting at the first %s", strings.ToLower(dimension[:len(dimension)-1]))
		}
		body["inheritFromBefore"] = inheritFromBefore
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{requestName: body})
}

func handleSheetsFormatRange(exec actions.Context, inv actions.Invocation) (any, error) {
	gridRange, err := sheetsRangeFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	format, fields, err := sheetsCellFormat(inv)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"repeatCell": map[string]any{
			"range":  gridRange,
			"cell":   map[string]any{"userEnteredFormat": format},
			"fields": strings.Join(fields, ","),
		},
	}
	return applyDedicatedSheetsRequest(exec, inv, request)
}

func handleSheetsMergeCells(exec actions.Context, inv actions.Invocation) (any, error) {
	gridRange, err := sheetsRangeFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	mergeType, err := sheetsEnum(inv.String("merge-type"), "MERGE_ALL", "MERGE_ALL", "MERGE_COLUMNS", "MERGE_ROWS")
	if err != nil {
		return nil, err
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{"mergeCells": map[string]any{"range": gridRange, "mergeType": mergeType}})
}

func handleSheetsUnmergeCells(exec actions.Context, inv actions.Invocation) (any, error) {
	gridRange, err := sheetsRangeFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{"unmergeCells": map[string]any{"range": gridRange}})
}

func handleSheetsProtectedRangesAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	gridRange, err := sheetsRangeFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	warningOnly := inv.Bool("warning-only")
	users := cleanSheetsRanges(inv.StringSlice("editor-user"))
	groups := cleanSheetsRanges(inv.StringSlice("editor-group"))
	domainUsers := inv.Bool("domain-users-can-edit")
	if warningOnly && (len(users) > 0 || len(groups) > 0 || domainUsers) {
		return nil, fmt.Errorf("warning-only protection cannot declare editors")
	}
	if !warningOnly && len(users) == 0 && len(groups) == 0 && !domainUsers {
		return nil, fmt.Errorf("hard protection requires --editor-user, --editor-group, or --domain-users-can-edit")
	}
	protectedRange := map[string]any{
		"range":       gridRange,
		"warningOnly": warningOnly,
	}
	if description := strings.TrimSpace(inv.String("description")); description != "" {
		protectedRange["description"] = description
	}
	if !warningOnly {
		editors := map[string]any{}
		if len(users) > 0 {
			editors["users"] = users
		}
		if len(groups) > 0 {
			editors["groups"] = groups
		}
		if domainUsers {
			editors["domainUsersCanEdit"] = true
		}
		protectedRange["editors"] = editors
	}
	return applyDedicatedSheetsRequest(exec, inv, map[string]any{"addProtectedRange": map[string]any{"protectedRange": protectedRange}})
}

func handleSheetsProtectedRangesDelete(exec actions.Context, inv actions.Invocation) (any, error) {
	protectedRangeID := inv.Int("protected-range-id")
	if protectedRangeID < 0 {
		return nil, fmt.Errorf("protected-range-id is required")
	}
	request := map[string]any{"deleteProtectedRange": map[string]any{"protectedRangeId": protectedRangeID}}
	return applyDedicatedSheetsRequest(exec, inv, request)
}

func applyDedicatedSheetsRequest(exec actions.Context, inv actions.Invocation, request map[string]any) (any, error) {
	spreadsheetID, err := googleSpreadsheetID(inv)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"requests": []map[string]any{request}}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, body), nil
	}
	return applySheetsBatchUpdate(exec, inv, spreadsheetID, body)
}

func requiredSheetID(inv actions.Invocation) (int, error) {
	sheetID := inv.Int("sheet-id")
	if sheetID < 0 {
		return 0, fmt.Errorf("sheet-id is required")
	}
	return sheetID, nil
}

func sheetsRangeFromInvocation(inv actions.Invocation) (map[string]any, error) {
	sheetID, err := requiredSheetID(inv)
	if err != nil {
		return nil, err
	}
	rangeName, err := requiredString(inv, "range")
	if err != nil {
		return nil, err
	}
	return sheetsGridRange(sheetID, rangeName)
}

func sheetsCellFormat(inv actions.Invocation) (map[string]any, []string, error) {
	if inv.Bool("clear-format") {
		if sheetsHasFormatFlags(inv) {
			return nil, nil, fmt.Errorf("clear-format cannot be combined with formatting flags")
		}
		return map[string]any{}, []string{"userEnteredFormat"}, nil
	}
	format := map[string]any{}
	fields := []string{}
	textFormat := map[string]any{}
	for _, pair := range []struct {
		set   string
		unset string
		field string
	}{
		{set: "bold", unset: "unbold", field: "bold"},
		{set: "italic", unset: "unitalic", field: "italic"},
		{set: "underline", unset: "no-underline", field: "underline"},
		{set: "strikethrough", unset: "no-strikethrough", field: "strikethrough"},
	} {
		if inv.Bool(pair.set) && inv.Bool(pair.unset) {
			return nil, nil, fmt.Errorf("%s and %s cannot be combined", pair.set, pair.unset)
		}
		if inv.Bool(pair.set) || inv.Bool(pair.unset) {
			textFormat[pair.field] = inv.Bool(pair.set)
			fields = append(fields, "userEnteredFormat.textFormat."+pair.field)
		}
	}
	if fontFamily := strings.TrimSpace(inv.String("font-family")); fontFamily != "" {
		textFormat["fontFamily"] = fontFamily
		fields = append(fields, "userEnteredFormat.textFormat.fontFamily")
	}
	if fontSize := inv.Int("font-size"); fontSize < 0 {
		return nil, nil, fmt.Errorf("font-size cannot be negative")
	} else if fontSize > 0 {
		textFormat["fontSize"] = fontSize
		fields = append(fields, "userEnteredFormat.textFormat.fontSize")
	}
	if foreground := strings.TrimSpace(inv.String("foreground-color")); foreground != "" {
		color, err := sheetsColorStyle(foreground)
		if err != nil {
			return nil, nil, err
		}
		textFormat["foregroundColorStyle"] = color
		fields = append(fields, "userEnteredFormat.textFormat.foregroundColorStyle")
	}
	if len(textFormat) > 0 {
		format["textFormat"] = textFormat
	}
	if background := strings.TrimSpace(inv.String("background-color")); background != "" {
		color, err := sheetsColorStyle(background)
		if err != nil {
			return nil, nil, err
		}
		format["backgroundColorStyle"] = color
		fields = append(fields, "userEnteredFormat.backgroundColorStyle")
	}
	for _, option := range []struct {
		flag    string
		field   string
		allowed []string
	}{
		{flag: "horizontal-alignment", field: "horizontalAlignment", allowed: []string{"LEFT", "CENTER", "RIGHT"}},
		{flag: "vertical-alignment", field: "verticalAlignment", allowed: []string{"TOP", "MIDDLE", "BOTTOM"}},
		{flag: "wrap-strategy", field: "wrapStrategy", allowed: []string{"OVERFLOW_CELL", "LEGACY_WRAP", "CLIP", "WRAP"}},
	} {
		if raw := strings.TrimSpace(inv.String(option.flag)); raw != "" {
			value, err := sheetsEnum(raw, "", option.allowed...)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", option.flag, err)
			}
			format[option.field] = value
			fields = append(fields, "userEnteredFormat."+option.field)
		}
	}
	numberType := strings.TrimSpace(inv.String("number-format-type"))
	numberPattern := strings.TrimSpace(inv.String("number-format-pattern"))
	if numberPattern != "" && numberType == "" {
		return nil, nil, fmt.Errorf("number-format-pattern requires number-format-type")
	}
	if numberType != "" {
		numberType, err := sheetsEnum(numberType, "", "TEXT", "NUMBER", "PERCENT", "CURRENCY", "DATE", "TIME", "DATE_TIME", "SCIENTIFIC")
		if err != nil {
			return nil, nil, fmt.Errorf("number-format-type: %w", err)
		}
		numberFormat := map[string]any{"type": numberType}
		if numberPattern != "" {
			numberFormat["pattern"] = numberPattern
		}
		format["numberFormat"] = numberFormat
		fields = append(fields, "userEnteredFormat.numberFormat")
	}
	if len(fields) == 0 {
		return nil, nil, fmt.Errorf("at least one formatting flag is required")
	}
	return format, fields, nil
}

func sheetsHasFormatFlags(inv actions.Invocation) bool {
	if slices.ContainsFunc([]string{
		"bold", "unbold", "italic", "unitalic", "underline", "no-underline",
		"strikethrough", "no-strikethrough",
	}, inv.Bool) {
		return true
	}
	for _, name := range []string{
		"font-family", "foreground-color", "background-color", "horizontal-alignment",
		"vertical-alignment", "wrap-strategy", "number-format-type", "number-format-pattern",
	} {
		if strings.TrimSpace(inv.String(name)) != "" {
			return true
		}
	}
	return inv.Int("font-size") != 0
}

func sheetsColorStyle(value string) (map[string]any, error) {
	color, err := docsColor(value)
	if err != nil {
		return nil, err
	}
	style, _ := color["color"].(map[string]any)
	return style, nil
}
