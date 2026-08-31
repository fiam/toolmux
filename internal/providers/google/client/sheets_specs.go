package client

import "github.com/fiam/toolmux/internal/actions"

func googleSheetsGroup() actions.Spec {
	return actions.Group("sheets",
		actions.Short("Use Google Sheets"),
		actions.Children(
			googleSheetsTool("sheets.create", "create", "Create a Google spreadsheet", actions.VerbCreate, actions.EffectWrite,
				actions.StringFlag("title", "", "spreadsheet title"),
				actions.StringSliceFlag("sheet", nil, "initial tab title; repeatable"),
				actions.StringFlag("locale", "", "spreadsheet locale, such as en_US"),
				actions.StringFlag("time-zone", "", "spreadsheet time zone, such as Europe/Lisbon"),
				actions.IntFlag("rows", 0, "initial row count; 0 uses the Google default"),
				actions.IntFlag("columns", 0, "initial column count; 0 uses the Google default"),
				actions.BoolFlag("dry-run", false, "show the Sheets create request without creating a spreadsheet"),
			),
			googleSheetsTool("sheets.get", "get", "Read Google spreadsheet metadata and tabs", actions.VerbRead, actions.EffectRead,
				append(sheetsTargetOptions("get [spreadsheet-id]"),
					actions.StringSliceFlag("range", nil, "optional A1 range to restrict returned data; repeatable"),
					actions.BoolFlag("include-grid-data", false, "include bounded cell grid data; requires --range"),
				)...,
			),
			actions.Group("values",
				actions.Short("Read and write Google Sheets values"),
				actions.Children(
					googleSheetsTool("sheets.values.get", "get", "Read values from one or more spreadsheet ranges", actions.VerbRead, actions.EffectRead,
						append(sheetsTargetOptions("get [spreadsheet-id]"),
							actions.StringSliceFlag("range", nil, "A1 range to read; repeatable"),
							actions.StringFlag("major-dimension", "ROWS", "result orientation: ROWS or COLUMNS"),
							actions.StringFlag("value-render-option", "FORMATTED_VALUE", "value rendering: FORMATTED_VALUE, UNFORMATTED_VALUE, or FORMULA"),
							actions.StringFlag("date-time-render-option", "SERIAL_NUMBER", "date/time rendering: SERIAL_NUMBER or FORMATTED_STRING"),
						)...,
					),
					googleSheetsToolWithEffects("sheets.values.update", "update", "Set values in a spreadsheet range", actions.VerbUpdate, actions.EffectWrite, actions.EffectRead,
						append(sheetsValueWriteOptions("update [spreadsheet-id]"),
							actions.StringFlag("range", "", "A1 range to update"),
						)...,
					),
					googleSheetsToolWithEffects("sheets.values.append", "append", "Append values to a spreadsheet range", actions.VerbUpdate, actions.EffectWrite, actions.EffectRead,
						append(sheetsValueWriteOptions("append [spreadsheet-id]"),
							actions.StringFlag("range", "", "A1 table range to append to"),
							actions.StringFlag("insert-data-option", "INSERT_ROWS", "append behavior: INSERT_ROWS or OVERWRITE"),
						)...,
					),
					googleSheetsTool("sheets.values.clear", "clear", "Clear values from spreadsheet ranges", actions.VerbDelete, actions.EffectWrite,
						append(sheetsTargetOptions("clear [spreadsheet-id]"),
							actions.StringSliceFlag("range", nil, "A1 range to clear; repeatable"),
							actions.BoolFlag("dry-run", false, "show the Sheets values:batchClear request without clearing values"),
						)...,
					),
					googleSheetsToolWithEffects("sheets.values.batch_update", "batch-update", "Set values in multiple spreadsheet ranges", actions.VerbUpdate, actions.EffectWrite, actions.EffectRead,
						append(sheetsTargetOptions("batch-update [spreadsheet-id]"),
							actions.StringFlag("json", "", "Sheets values:batchUpdate object, data array, or @path"),
							actions.StringFlag("value-input-option", "RAW", "default input handling: RAW or USER_ENTERED"),
							actions.BoolFlag("dry-run", false, "show the Sheets values:batchUpdate request without applying it"),
						)...,
					),
				),
			),
			sheetsTabsGroup(),
			sheetsDimensionGroup("rows", "row", "sheets.rows.insert", "sheets.rows.delete"),
			sheetsDimensionGroup("columns", "column", "sheets.columns.insert", "sheets.columns.delete"),
			googleSheetsTool("sheets.format_range", "format-range", "Format cells in a spreadsheet range", actions.VerbUpdate, actions.EffectWrite,
				append(sheetsGridRangeOptions("format-range [spreadsheet-id]"), sheetsFormatOptions()...)...,
			),
			googleSheetsTool("sheets.merge_cells", "merge-cells", "Merge cells in a spreadsheet range", actions.VerbUpdate, actions.EffectWrite,
				append(sheetsGridRangeOptions("merge-cells [spreadsheet-id]"),
					actions.StringFlag("merge-type", "MERGE_ALL", "merge type: MERGE_ALL, MERGE_COLUMNS, or MERGE_ROWS"),
				)...,
			),
			googleSheetsTool("sheets.unmerge_cells", "unmerge-cells", "Unmerge cells in a spreadsheet range", actions.VerbUpdate, actions.EffectWrite,
				sheetsGridRangeOptions("unmerge-cells [spreadsheet-id]")...,
			),
			sheetsProtectedRangesGroup(),
			googleSheetsToolWithEffects("sheets.batch_update", "batch-update", "Apply raw Google Sheets batchUpdate requests", actions.VerbUpdate, actions.EffectWrite, actions.EffectRead,
				append(sheetsTargetOptions("batch-update [spreadsheet-id]"),
					actions.StringFlag("json", "", "Sheets batchUpdate object, requests array, or @path"),
					actions.BoolFlag("dry-run", false, "show the Sheets batchUpdate request without applying it"),
				)...,
			),
		),
	)
}

func sheetsTabsGroup() actions.Spec {
	return actions.Group("tabs",
		actions.Short("Manage spreadsheet tabs"),
		actions.Children(
			googleSheetsTool("sheets.tabs.add", "add", "Add a tab to a spreadsheet", actions.VerbCreate, actions.EffectWrite,
				append(sheetsTargetOptions("add [spreadsheet-id]"),
					actions.StringFlag("title", "", "new tab title"),
					actions.IntFlag("rows", 0, "row count; 0 uses the Google default"),
					actions.IntFlag("columns", 0, "column count; 0 uses the Google default"),
					actions.BoolFlag("dry-run", false, "show the addSheet request without applying it"),
				)...,
			),
			googleSheetsTool("sheets.tabs.delete", "delete", "Delete a spreadsheet tab", actions.VerbDelete, actions.EffectWrite,
				append(sheetsTargetOptions("delete [spreadsheet-id]"),
					actions.IntFlag("sheet-id", -1, "numeric sheet ID to delete"),
					actions.BoolFlag("dry-run", false, "show the deleteSheet request without applying it"),
				)...,
			),
			googleSheetsTool("sheets.tabs.rename", "rename", "Rename a spreadsheet tab", actions.VerbUpdate, actions.EffectWrite,
				append(sheetsTargetOptions("rename [spreadsheet-id]"),
					actions.IntFlag("sheet-id", -1, "numeric sheet ID to rename"),
					actions.StringFlag("title", "", "new tab title"),
					actions.BoolFlag("dry-run", false, "show the updateSheetProperties request without applying it"),
				)...,
			),
		),
	)
}

func sheetsDimensionGroup(segment, singular, insertID, deleteID string) actions.Spec {
	return actions.Group(segment,
		actions.Short("Insert or delete spreadsheet "+segment),
		actions.Children(
			googleSheetsTool(insertID, "insert", "Insert spreadsheet "+segment, actions.VerbCreate, actions.EffectWrite,
				append(sheetsTargetOptions("insert [spreadsheet-id]"),
					actions.IntFlag("sheet-id", -1, "numeric sheet ID"),
					actions.IntFlag("start", 1, "one-based first "+singular+" position"),
					actions.IntFlag("count", 1, "number of "+segment+" to insert"),
					actions.BoolFlag("inherit-from-before", false, "inherit formatting from the preceding "+singular),
					actions.BoolFlag("dry-run", false, "show the insertDimension request without applying it"),
				)...,
			),
			googleSheetsTool(deleteID, "delete", "Delete spreadsheet "+segment, actions.VerbDelete, actions.EffectWrite,
				append(sheetsTargetOptions("delete [spreadsheet-id]"),
					actions.IntFlag("sheet-id", -1, "numeric sheet ID"),
					actions.IntFlag("start", 1, "one-based first "+singular+" position"),
					actions.IntFlag("count", 1, "number of "+segment+" to delete"),
					actions.BoolFlag("dry-run", false, "show the deleteDimension request without applying it"),
				)...,
			),
		),
	)
}

func sheetsProtectedRangesGroup() actions.Spec {
	return actions.Group("protected-ranges",
		actions.Short("Manage protected spreadsheet ranges"),
		actions.Children(
			googleSheetsTool("sheets.protected_ranges.add", "add", "Protect a spreadsheet range", actions.VerbCreate, actions.EffectWrite,
				append(sheetsGridRangeOptions("add [spreadsheet-id]"),
					actions.StringFlag("description", "", "protection description"),
					actions.BoolFlag("warning-only", false, "warn on edits instead of restricting editors"),
					actions.StringSliceFlag("editor-user", nil, "user email allowed to edit; repeatable"),
					actions.StringSliceFlag("editor-group", nil, "group email allowed to edit; repeatable"),
					actions.BoolFlag("domain-users-can-edit", false, "allow users in the document domain to edit"),
				)...,
			),
			googleSheetsTool("sheets.protected_ranges.delete", "delete", "Delete a protected spreadsheet range", actions.VerbDelete, actions.EffectWrite,
				append(sheetsTargetOptions("delete [spreadsheet-id]"),
					actions.IntFlag("protected-range-id", -1, "numeric protected range ID"),
					actions.BoolFlag("dry-run", false, "show the deleteProtectedRange request without applying it"),
				)...,
			),
		),
	)
}

func sheetsTargetOptions(use string) []actions.Option {
	return []actions.Option{
		actions.Use(use),
		actions.MaxArgs(1),
		actions.StringFlag("spreadsheet-id", "", "Google spreadsheet ID or URL"),
	}
}

func sheetsValueWriteOptions(use string) []actions.Option {
	return append(sheetsTargetOptions(use),
		actions.StringFlag("values-json", "", "two-dimensional JSON values array or @path"),
		actions.StringFlag("file", "", "JSON, CSV, or TSV file containing values"),
		actions.StringFlag("input-format", "auto", "file input format: auto, json, csv, or tsv"),
		actions.StringFlag("major-dimension", "ROWS", "input orientation: ROWS or COLUMNS"),
		actions.StringFlag("value-input-option", "RAW", "input handling: RAW or USER_ENTERED"),
		actions.BoolFlag("include-values-in-response", false, "include written values in the response"),
		actions.StringFlag("response-value-render-option", "FORMATTED_VALUE", "response rendering: FORMATTED_VALUE, UNFORMATTED_VALUE, or FORMULA"),
		actions.StringFlag("response-date-time-render-option", "SERIAL_NUMBER", "response date/time rendering: SERIAL_NUMBER or FORMATTED_STRING"),
		actions.BoolFlag("dry-run", false, "show the Sheets values request without applying it"),
	)
}

func sheetsGridRangeOptions(use string) []actions.Option {
	return append(sheetsTargetOptions(use),
		actions.IntFlag("sheet-id", -1, "numeric sheet ID"),
		actions.StringFlag("range", "", "unqualified A1 range, such as A1:D5, A:D, or 1:5"),
		actions.BoolFlag("dry-run", false, "show the Sheets batchUpdate request without applying it"),
	)
}

func sheetsFormatOptions() []actions.Option {
	return []actions.Option{
		actions.BoolFlag("bold", false, "set bold text"),
		actions.BoolFlag("unbold", false, "clear bold text"),
		actions.BoolFlag("italic", false, "set italic text"),
		actions.BoolFlag("unitalic", false, "clear italic text"),
		actions.BoolFlag("underline", false, "set underlined text"),
		actions.BoolFlag("no-underline", false, "clear underlined text"),
		actions.BoolFlag("strikethrough", false, "set strikethrough text"),
		actions.BoolFlag("no-strikethrough", false, "clear strikethrough text"),
		actions.StringFlag("font-family", "", "font family"),
		actions.IntFlag("font-size", 0, "font size in points"),
		actions.StringFlag("foreground-color", "", "text color as #RRGGBB"),
		actions.StringFlag("background-color", "", "cell background color as #RRGGBB"),
		actions.StringFlag("horizontal-alignment", "", "LEFT, CENTER, or RIGHT alignment"),
		actions.StringFlag("vertical-alignment", "", "TOP, MIDDLE, or BOTTOM alignment"),
		actions.StringFlag("wrap-strategy", "", "OVERFLOW_CELL, LEGACY_WRAP, CLIP, or WRAP"),
		actions.StringFlag("number-format-type", "", "TEXT, NUMBER, PERCENT, CURRENCY, DATE, TIME, DATE_TIME, or SCIENTIFIC"),
		actions.StringFlag("number-format-pattern", "", "Sheets number format pattern"),
		actions.BoolFlag("clear-format", false, "clear all user-entered formatting in the range"),
	}
}

func googleSheetsTool(localID, segment, short string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	return googleSheetsToolWithEffects(localID, segment, short, verb, remote, actions.EffectNone, opts...)
}

func googleSheetsToolWithEffects(localID, segment, short string, verb actions.Verb, remote, local actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.Description(sheetsToolDescription(localID, short)),
		actions.RBAC(actions.ResourceName("spreadsheet"), verb, remote, local),
		actions.Scopes(defaultDriveScopes...),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(localID), segment, base...)
}

func sheetsToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"sheets.create":                  "Create a Google spreadsheet in My Drive using the non-sensitive drive.file scope. The new file is immediately accessible to Toolmux.",
		"sheets.get":                     "Read metadata, tab IDs, grid sizes, protected ranges, and optionally bounded grid data for a Google spreadsheet visible to Toolmux.",
		"sheets.values.get":              "Read one or more A1 ranges with Sheets values:batchGet. Existing spreadsheets must first be selected for Toolmux through Google Picker.",
		"sheets.values.update":           "Set a single A1 range from inline JSON or a JSON, CSV, or TSV file. RAW is the safe default; use USER_ENTERED to interpret formulas, dates, and locale-sensitive numbers.",
		"sheets.values.append":           "Append JSON, CSV, or TSV values to an A1 table range. RAW is the default input mode and INSERT_ROWS is the default append behavior.",
		"sheets.values.clear":            "Clear values, but not formatting, from one or more A1 ranges using Sheets values:batchClear.",
		"sheets.values.batch_update":     "Set multiple A1 ranges through Sheets values:batchUpdate. Pass the full request object, a data array, or @path.",
		"sheets.tabs.add":                "Add a tab with a required title and optional initial grid size. The reply includes Google's numeric sheet ID for later structural commands.",
		"sheets.tabs.delete":             "Delete one tab by numeric sheet ID. This removes the tab and its cells; use --dry-run to inspect the deleteSheet request first.",
		"sheets.tabs.rename":             "Rename one tab by numeric sheet ID without changing its cell values or formatting.",
		"sheets.rows.insert":             "Insert a contiguous row range at a one-based position on a numeric sheet ID, optionally inheriting formatting from the preceding row.",
		"sheets.rows.delete":             "Delete a contiguous row range from a one-based position on a numeric sheet ID, shifting later rows upward.",
		"sheets.columns.insert":          "Insert a contiguous column range at a one-based position on a numeric sheet ID, optionally inheriting formatting from the preceding column.",
		"sheets.columns.delete":          "Delete a contiguous column range from a one-based position on a numeric sheet ID, shifting later columns left.",
		"sheets.format_range":            "Apply common text, color, alignment, wrapping, and number formatting to an unqualified A1 range on a numeric sheet ID.",
		"sheets.merge_cells":             "Merge an unqualified A1 range on a numeric sheet ID by rows, columns, or as one cell. Only the top-left value is retained by Google.",
		"sheets.unmerge_cells":           "Unmerge every merged cell intersecting an unqualified A1 range on a numeric sheet ID.",
		"sheets.batch_update":            "Apply raw Sheets spreadsheets.batchUpdate requests. Pass the full object, a requests array, or @path; requests in one call are validated and applied atomically by Google.",
		"sheets.protected_ranges.add":    "Add warning-only protection or hard protection with explicit user, group, or domain editors to an unqualified A1 range.",
		"sheets.protected_ranges.delete": "Delete a protected range by numeric protectedRangeId returned by Google Sheets metadata or batchUpdate replies.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}
