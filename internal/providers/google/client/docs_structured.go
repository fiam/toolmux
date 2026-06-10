package client

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func handleDocsFindStructure(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(inv.String("kind"), "all")))
	query := inv.String("text")
	matches, err := findDocumentStructure(document, kind, query, inv.Bool("match-case"))
	if err != nil {
		return nil, err
	}
	return docsStructureResult{
		DocumentID: document.DocumentID,
		Title:      document.Title,
		RevisionID: document.RevisionID,
		Matches:    matches,
	}, nil
}

func handleDocsStyleRanges(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	requests, err := docsStyleRangeRequests(exec, inv, documentID)
	if err != nil {
		return nil, err
	}
	request := docsBatchRequest(requests, docsWriteControl(inv))
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return docsBatchUpdateResult(response), nil
}

func handleDocsInsertTable(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	rows := inv.Int("rows")
	columns := inv.Int("columns")
	if rows <= 0 {
		return nil, fmt.Errorf("rows must be greater than zero")
	}
	if columns <= 0 {
		return nil, fmt.Errorf("columns must be greater than zero")
	}
	requests, err := docsInsertTableRequests(inv, rows, columns)
	if err != nil {
		return nil, err
	}
	request := docsBatchRequest(requests, docsWriteControl(inv))
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return docsBatchUpdateResult(response), nil
}

func handleDocsInsertImage(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	in, err := resolveImageInput(exec, inv)
	if err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		request := docsBatchRequest([]map[string]any{docsInsertImageRequest(inv, dryRunImageURI(inv, in))}, docsWriteControl(inv))
		return actions.NewDryRun(inv.Spec.ID, map[string]any{
			"batchUpdate": request,
			"image":       in,
		}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	uri, meta, cleanup, err := publishImage(exec, inv, client, in)
	if err != nil {
		return nil, err
	}
	request := docsBatchRequest([]map[string]any{docsInsertImageRequest(inv, uri)}, docsWriteControl(inv))
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	runImageCleanup(exec, cleanup, meta)
	return docsInsertImageResult{
		DocumentID:      response.DocumentID,
		ImageURI:        uri,
		Publish:         meta,
		WriteControl:    response.WriteControl,
		AppliedRequests: response.AppliedRequests,
	}, nil
}

func handleDocsExport(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	format, mimeType, err := docsExportFormat(inv)
	if err != nil {
		return nil, err
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	content, err := client.ExportDriveFile(exec.Context, documentID, mimeType)
	if err != nil {
		return nil, err
	}
	return newDocsExportResult(documentID, format, mimeType, content), nil
}

func docsStyleRangeRequests(exec actions.Context, inv actions.Invocation, documentID string) ([]map[string]any, error) {
	ranges, err := docsTargetRanges(exec, inv, documentID)
	if err != nil {
		return nil, err
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("no matching ranges found")
	}
	textStyle, textFields, err := docsTextStyle(inv)
	if err != nil {
		return nil, err
	}
	paragraphStyle, paragraphFields := docsParagraphStyle(inv)
	bulletPreset := strings.TrimSpace(inv.String("bullet-preset"))
	if len(textFields) == 0 && len(paragraphFields) == 0 && bulletPreset == "" {
		return nil, fmt.Errorf("at least one style flag is required")
	}
	requests := []map[string]any{}
	for _, target := range ranges {
		if len(textFields) > 0 {
			requests = append(requests, map[string]any{
				"updateTextStyle": map[string]any{
					"range":     target,
					"textStyle": textStyle,
					"fields":    strings.Join(textFields, ","),
				},
			})
		}
		if len(paragraphFields) > 0 {
			requests = append(requests, map[string]any{
				"updateParagraphStyle": map[string]any{
					"range":          target,
					"paragraphStyle": paragraphStyle,
					"fields":         strings.Join(paragraphFields, ","),
				},
			})
		}
		if bulletPreset != "" {
			requests = append(requests, map[string]any{
				"createParagraphBullets": map[string]any{
					"range":        target,
					"bulletPreset": bulletPreset,
				},
			})
		}
	}
	return requests, nil
}

func docsTargetRanges(exec actions.Context, inv actions.Invocation, documentID string) ([]map[string]any, error) {
	start := inv.Int("start-index")
	end := inv.Int("end-index")
	if start > 0 || end > 0 {
		if start <= 0 || end <= start {
			return nil, fmt.Errorf("start-index and end-index must define a positive non-empty range")
		}
		return []map[string]any{docsRange(start, end)}, nil
	}
	targetStyle := strings.TrimSpace(inv.String("paragraph-style-type"))
	text := strings.TrimSpace(inv.String("text"))
	if targetStyle == "" && text == "" {
		return nil, fmt.Errorf("pass --start-index/--end-index, --text, or --paragraph-style-type")
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	ranges := []map[string]any{}
	if text != "" {
		for _, match := range textMatches(document.Body.Content, text, inv.Bool("match-case")) {
			ranges = append(ranges, docsRange(match.StartIndex, match.EndIndex))
		}
	}
	if targetStyle != "" {
		for _, match := range paragraphMatchesByStyle(document.Body.Content, normalizeNamedStyleType(targetStyle)) {
			ranges = append(ranges, docsRange(match.StartIndex, match.EndIndex))
		}
	}
	return ranges, nil
}

func docsTextStyle(inv actions.Invocation) (map[string]any, []string, error) {
	style := map[string]any{}
	fields := []string{}
	if inv.Bool("bold") {
		style["bold"] = true
		fields = append(fields, "bold")
	}
	if inv.Bool("italic") {
		style["italic"] = true
		fields = append(fields, "italic")
	}
	if inv.Bool("underline") {
		style["underline"] = true
		fields = append(fields, "underline")
	}
	if value := strings.TrimSpace(inv.String("foreground-color")); value != "" {
		color, err := docsColor(value)
		if err != nil {
			return nil, nil, err
		}
		style["foregroundColor"] = color
		fields = append(fields, "foregroundColor")
	}
	if value := strings.TrimSpace(inv.String("background-color")); value != "" {
		color, err := docsColor(value)
		if err != nil {
			return nil, nil, err
		}
		style["backgroundColor"] = color
		fields = append(fields, "backgroundColor")
	}
	return style, fields, nil
}

func docsParagraphStyle(inv actions.Invocation) (map[string]any, []string) {
	style := map[string]any{}
	fields := []string{}
	if value := strings.TrimSpace(inv.String("named-style-type")); value != "" {
		style["namedStyleType"] = normalizeNamedStyleType(value)
		fields = append(fields, "namedStyleType")
	}
	if value := strings.TrimSpace(inv.String("alignment")); value != "" {
		style["alignment"] = strings.ToUpper(value)
		fields = append(fields, "alignment")
	}
	return style, fields
}

func docsInsertTableRequests(inv actions.Invocation, rows, columns int) ([]map[string]any, error) {
	insert := map[string]any{
		"rows":    rows,
		"columns": columns,
	}
	index := inv.Int("index")
	if index > 0 {
		insert["location"] = map[string]any{"index": index}
	} else {
		insert["endOfSegmentLocation"] = map[string]any{}
	}
	requests := []map[string]any{{"insertTable": insert}}
	if color := strings.TrimSpace(inv.String("cell-background-color")); color != "" {
		if index <= 0 {
			return nil, fmt.Errorf("index is required when styling an inserted table")
		}
		docsColor, err := docsColor(color)
		if err != nil {
			return nil, err
		}
		requests = append(requests, map[string]any{
			"updateTableCellStyle": map[string]any{
				"tableRange": map[string]any{
					"tableCellLocation": map[string]any{
						"tableStartLocation": map[string]any{"index": index + 1},
						"rowIndex":           0,
						"columnIndex":        0,
					},
					"rowSpan":    rows,
					"columnSpan": columns,
				},
				"tableCellStyle": map[string]any{
					"backgroundColor": docsColor,
				},
				"fields": "backgroundColor",
			},
		})
	}
	return requests, nil
}

func docsInsertImageRequest(inv actions.Invocation, uri string) map[string]any {
	insert := map[string]any{"uri": uri}
	if index := inv.Int("index"); index > 0 {
		insert["location"] = map[string]any{"index": index}
	} else {
		insert["endOfSegmentLocation"] = map[string]any{}
	}
	if size := docsObjectSize(inv); len(size) > 0 {
		insert["objectSize"] = size
	}
	return map[string]any{"insertInlineImage": insert}
}

func docsObjectSize(inv actions.Invocation) map[string]any {
	size := map[string]any{}
	if width := inv.Int("width-pt"); width > 0 {
		size["width"] = map[string]any{"magnitude": width, "unit": "PT"}
	}
	if height := inv.Int("height-pt"); height > 0 {
		size["height"] = map[string]any{"magnitude": height, "unit": "PT"}
	}
	return size
}

func docsExportFormat(inv actions.Invocation) (string, string, error) {
	mimeType := strings.TrimSpace(inv.String("mime-type"))
	format := strings.ToLower(strings.TrimSpace(firstNonEmpty(inv.String("format"), "markdown")))
	if mimeType != "" {
		return firstNonEmpty(format, "custom"), mimeType, nil
	}
	formats := map[string]string{
		"docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"odt":      "application/vnd.oasis.opendocument.text",
		"rtf":      "application/rtf",
		"pdf":      "application/pdf",
		"text":     "text/plain",
		"txt":      "text/plain",
		"html":     "application/zip",
		"zip":      "application/zip",
		"epub":     "application/epub+zip",
		"markdown": "text/markdown",
		"md":       "text/markdown",
	}
	mimeType, ok := formats[format]
	if !ok {
		return "", "", fmt.Errorf("unsupported export format %q", format)
	}
	return format, mimeType, nil
}

func isTextExportMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "text/plain", "text/markdown", "application/rtf":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "text/")
	}
}

func findDocumentStructure(document googleapi.Document, kind, query string, matchCase bool) ([]docsStructureMatch, error) {
	if kind == "" {
		kind = "all"
	}
	switch kind {
	case "all", "paragraph", "paragraphs", "heading", "headings", "table", "tables", "text", "image", "images":
	default:
		return nil, fmt.Errorf("unsupported structure kind %q", kind)
	}
	if kind == "image" || kind == "images" {
		return filterImageMatches(documentImages(document), query, matchCase), nil
	}
	if kind == "text" && query == "" {
		return textRuns(document.Body.Content), nil
	}
	if query != "" && (kind == "all" || kind == "text") {
		return textMatches(document.Body.Content, query, matchCase), nil
	}
	matches := []docsStructureMatch{}
	walkStructuralElements(document.Body.Content, func(element googleapi.StructuralElement) {
		switch {
		case element.Paragraph != nil:
			style := element.Paragraph.ParagraphStyle
			isHeading := strings.HasPrefix(style.NamedStyleType, "HEADING_") || style.NamedStyleType == "TITLE" || style.NamedStyleType == "SUBTITLE"
			if kind == "all" || kind == "paragraph" || kind == "paragraphs" || (isHeading && (kind == "heading" || kind == "headings")) {
				matches = append(matches, docsStructureMatch{
					Kind:           paragraphKind(style),
					StartIndex:     element.StartIndex,
					EndIndex:       element.EndIndex,
					NamedStyleType: style.NamedStyleType,
					HeadingID:      style.HeadingID,
					Text:           paragraphText(*element.Paragraph),
				})
			}
		case element.Table != nil:
			if kind == "all" || kind == "table" || kind == "tables" {
				matches = append(matches, docsStructureMatch{
					Kind:       "table",
					StartIndex: element.StartIndex,
					EndIndex:   element.EndIndex,
					Rows:       element.Table.Rows,
					Columns:    element.Table.Columns,
					Text:       tableText(*element.Table),
				})
			}
		}
	})
	if kind == "all" {
		matches = append(matches, documentImages(document)...)
	}
	if query == "" {
		return matches, nil
	}
	filtered := matches[:0]
	for _, match := range matches {
		if textContains(match.Text, query, matchCase) {
			filtered = append(filtered, match)
		}
	}
	return filtered, nil
}

// documentImages returns one match per image in the document: inline images
// (with the body range deleteContentRange needs) and positioned images (with
// their paragraph anchor), each carrying the object ID and looked-up image
// properties from the document-level inlineObjects/positionedObjects maps.
func documentImages(document googleapi.Document) []docsStructureMatch {
	matches := []docsStructureMatch{}
	walkStructuralElements(document.Body.Content, func(element googleapi.StructuralElement) {
		if element.Paragraph == nil {
			return
		}
		for _, child := range element.Paragraph.Elements {
			if child.InlineObjectElement == nil || child.InlineObjectElement.InlineObjectID == "" {
				continue
			}
			id := child.InlineObjectElement.InlineObjectID
			match := docsStructureMatch{
				Kind:       "inline_image",
				StartIndex: child.StartIndex,
				EndIndex:   child.EndIndex,
				ObjectID:   id,
			}
			if object, ok := document.InlineObjects[id]; ok {
				applyEmbeddedObject(&match, object.InlineObjectProperties.EmbeddedObject)
			}
			matches = append(matches, match)
		}
		for _, id := range element.Paragraph.PositionedObjectIds {
			match := docsStructureMatch{
				Kind:       "positioned_image",
				StartIndex: element.StartIndex,
				EndIndex:   element.EndIndex,
				ObjectID:   id,
			}
			if object, ok := document.PositionedObjects[id]; ok {
				applyEmbeddedObject(&match, object.PositionedObjectProperties.EmbeddedObject)
			}
			matches = append(matches, match)
		}
	})
	return matches
}

func applyEmbeddedObject(match *docsStructureMatch, object googleapi.EmbeddedObject) {
	match.Title = object.Title
	match.Description = object.Description
	if object.ImageProperties != nil {
		match.ContentURI = object.ImageProperties.ContentURI
		match.SourceURI = object.ImageProperties.SourceURI
	}
	if object.Size != nil {
		match.WidthPt = object.Size.Width.Magnitude
		match.HeightPt = object.Size.Height.Magnitude
	}
}

// filterImageMatches keeps images whose object ID, title, description, or
// source/content URI contains the query (no query returns everything).
func filterImageMatches(images []docsStructureMatch, query string, matchCase bool) []docsStructureMatch {
	if strings.TrimSpace(query) == "" {
		return images
	}
	filtered := make([]docsStructureMatch, 0, len(images))
	for _, image := range images {
		if textContains(image.ObjectID, query, matchCase) ||
			textContains(image.Title, query, matchCase) ||
			textContains(image.Description, query, matchCase) ||
			textContains(image.SourceURI, query, matchCase) ||
			textContains(image.ContentURI, query, matchCase) {
			filtered = append(filtered, image)
		}
	}
	return filtered
}

func textMatches(elements []googleapi.StructuralElement, query string, matchCase bool) []docsStructureMatch {
	matches := []docsStructureMatch{}
	walkParagraphs(elements, func(paragraph googleapi.Paragraph) {
		for _, element := range paragraph.Elements {
			if element.TextRun == nil || element.TextRun.Content == "" {
				continue
			}
			matches = append(matches, textRunMatches(element, query, matchCase)...)
		}
	})
	return matches
}

func textRuns(elements []googleapi.StructuralElement) []docsStructureMatch {
	matches := []docsStructureMatch{}
	walkParagraphs(elements, func(paragraph googleapi.Paragraph) {
		for _, element := range paragraph.Elements {
			if element.TextRun == nil || element.TextRun.Content == "" {
				continue
			}
			matches = append(matches, docsStructureMatch{
				Kind:       "text",
				StartIndex: element.StartIndex,
				EndIndex:   element.EndIndex,
				Text:       element.TextRun.Content,
			})
		}
	})
	return matches
}

func textRunMatches(element googleapi.ParagraphElement, query string, matchCase bool) []docsStructureMatch {
	haystack := []rune(element.TextRun.Content)
	needle := []rune(query)
	if len(needle) == 0 || len(needle) > len(haystack) {
		return nil
	}
	matches := []docsStructureMatch{}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		candidate := string(haystack[i : i+len(needle)])
		if (matchCase && candidate != query) || (!matchCase && !strings.EqualFold(candidate, query)) {
			continue
		}
		prefix := string(haystack[:i])
		start := element.StartIndex + utf16Units(prefix)
		matches = append(matches, docsStructureMatch{
			Kind:       "text",
			StartIndex: start,
			EndIndex:   start + utf16Units(candidate),
			Text:       candidate,
		})
	}
	return matches
}

func paragraphMatchesByStyle(elements []googleapi.StructuralElement, namedStyleType string) []docsStructureMatch {
	matches := []docsStructureMatch{}
	walkStructuralElements(elements, func(element googleapi.StructuralElement) {
		if element.Paragraph == nil {
			return
		}
		style := element.Paragraph.ParagraphStyle
		if style.NamedStyleType != namedStyleType {
			return
		}
		matches = append(matches, docsStructureMatch{
			Kind:           paragraphKind(style),
			StartIndex:     element.StartIndex,
			EndIndex:       element.EndIndex,
			NamedStyleType: style.NamedStyleType,
			HeadingID:      style.HeadingID,
			Text:           paragraphText(*element.Paragraph),
		})
	})
	return matches
}

func walkStructuralElements(elements []googleapi.StructuralElement, visit func(googleapi.StructuralElement)) {
	for _, element := range elements {
		visit(element)
		if element.Table == nil {
			continue
		}
		for _, row := range element.Table.TableRows {
			for _, cell := range row.TableCells {
				walkStructuralElements(cell.Content, visit)
			}
		}
	}
}

func walkParagraphs(elements []googleapi.StructuralElement, visit func(googleapi.Paragraph)) {
	walkStructuralElements(elements, func(element googleapi.StructuralElement) {
		if element.Paragraph != nil {
			visit(*element.Paragraph)
		}
	})
}

func paragraphText(paragraph googleapi.Paragraph) string {
	var builder strings.Builder
	for _, element := range paragraph.Elements {
		if element.TextRun != nil {
			builder.WriteString(element.TextRun.Content)
		}
	}
	return builder.String()
}

func tableText(table googleapi.Table) string {
	var builder strings.Builder
	for _, row := range table.TableRows {
		for _, cell := range row.TableCells {
			builder.WriteString(cellText(cell))
		}
	}
	return builder.String()
}

func cellText(cell googleapi.TableCell) string {
	var builder strings.Builder
	walkParagraphs(cell.Content, func(paragraph googleapi.Paragraph) {
		builder.WriteString(paragraphText(paragraph))
	})
	return builder.String()
}

func paragraphKind(style googleapi.ParagraphStyle) string {
	if strings.HasPrefix(style.NamedStyleType, "HEADING_") || style.NamedStyleType == "TITLE" || style.NamedStyleType == "SUBTITLE" {
		return "heading"
	}
	return "paragraph"
}

func textContains(value, query string, matchCase bool) bool {
	if matchCase {
		return strings.Contains(value, query)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func docsBatchRequest(requests []map[string]any, writeControl *googleapi.WriteControl) map[string]any {
	values := make([]any, 0, len(requests))
	for _, request := range requests {
		values = append(values, request)
	}
	body := map[string]any{"requests": values}
	if writeControl != nil {
		body["writeControl"] = writeControl
	}
	return body
}

func docsRange(start, end int) map[string]any {
	return map[string]any{"startIndex": start, "endIndex": end}
}

func docsColor(value string) (map[string]any, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return nil, fmt.Errorf("color must be #RRGGBB")
	}
	var red, green, blue uint8
	if _, err := fmt.Sscanf(value, "%02x%02x%02x", &red, &green, &blue); err != nil {
		return nil, fmt.Errorf("color must be #RRGGBB")
	}
	return map[string]any{
		"color": map[string]any{
			"rgbColor": map[string]any{
				"red":   float64(red) / 255,
				"green": float64(green) / 255,
				"blue":  float64(blue) / 255,
			},
		},
	}, nil
}

func normalizeNamedStyleType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func utf16Units(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func filepathBase(value string) string {
	value = strings.TrimRight(value, "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		return value[index+1:]
	}
	return value
}
