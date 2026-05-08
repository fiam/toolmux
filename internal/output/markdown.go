package output

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
)

type MarkdownTheme string

const (
	MarkdownDark  MarkdownTheme = "dark"
	MarkdownLight MarkdownTheme = "light"
	MarkdownPlain MarkdownTheme = "plain"
)

type MarkdownOptions struct {
	Width int
	Theme MarkdownTheme
}

type MarkdownLink struct {
	Label string
	URL   string
}

var (
	markdownLinkPattern       = regexp.MustCompile(`!?\[([^\]\n]+)\]\(([^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\)`)
	notionPageTagPattern      = regexp.MustCompile(`(?is)<page\b([^>]*)>(.*?)</page>`)
	notionURLAttributePattern = regexp.MustCompile(`(?is)\burl\s*=\s*(?:"([^"]+)"|'([^']+)')`)
	notionEmptyBlockPattern   = regexp.MustCompile(`(?is)<empty-block\s*/>`)
	htmlTagPattern            = regexp.MustCompile(`(?s)<[^>]+>`)
	linkReferenceSuffix       = regexp.MustCompile(`\[\d+\]\*\*$`)
)

func RenderMarkdown(source string, opts MarkdownOptions) (string, error) {
	width := opts.Width
	if width <= 0 {
		width = 100
	}
	style := styles.DarkStyle
	switch opts.Theme {
	case MarkdownLight:
		style = styles.LightStyle
	case MarkdownPlain:
		style = styles.NoTTYStyle
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	defer renderer.Close()
	rendered, err := renderer.Render(strings.TrimRight(source, "\n"))
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func PrepareReadableMarkdown(source string) string {
	source = NormalizeNotionEnhancedMarkdown(source)
	annotated, links := AnnotateMarkdownLinks(source)
	annotated = ListStandaloneLinkReferences(annotated)
	annotated = PreserveSoftLineBreaks(annotated)
	if len(links) == 0 {
		return annotated
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(annotated, "\n"))
	b.WriteString("\n\n## Links\n\n")
	for i, link := range links {
		label := strings.TrimSpace(link.Label)
		if label == "" {
			label = link.URL
		}
		fmt.Fprintf(&b, "%d. %s - %s\n", i+1, label, link.URL)
	}
	return b.String()
}

func ExtractMarkdownLinks(source string) []MarkdownLink {
	source = NormalizeNotionEnhancedMarkdown(source)
	_, links := AnnotateMarkdownLinks(source)
	return links
}

func NormalizeNotionEnhancedMarkdown(source string) string {
	normalized := notionPageTagPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := notionPageTagPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		url := notionPageURL(parts[1])
		if url == "" {
			return match
		}
		label := strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(parts[2], "")))
		if label == "" {
			label = url
		}
		return fmt.Sprintf("[%s](%s)", escapeMarkdownLinkLabel(label), url)
	})
	return notionEmptyBlockPattern.ReplaceAllString(normalized, "\n")
}

func AnnotateMarkdownLinks(source string) (string, []MarkdownLink) {
	var links []MarkdownLink
	annotated := markdownLinkPattern.ReplaceAllStringFunc(source, func(match string) string {
		if strings.HasPrefix(match, "![") {
			return match
		}
		parts := markdownLinkPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		label := strings.TrimSpace(parts[1])
		url := strings.TrimSpace(parts[2])
		if url == "" || strings.HasPrefix(url, "#") {
			return match
		}
		links = append(links, MarkdownLink{Label: label, URL: url})
		return fmt.Sprintf("**%s [%d]**", escapeMarkdownEmphasis(parts[1]), len(links))
	})
	return annotated, links
}

func ListStandaloneLinkReferences(source string) string {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !standaloneLinkReference(trimmed) {
			continue
		}
		prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = prefix + "- " + trimmed
	}
	return strings.Join(lines, "\n")
}

func PreserveSoftLineBreaks(source string) string {
	lines := strings.Split(source, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" || i == len(lines)-1 || strings.TrimSpace(lines[i+1]) == "" || markdownListItem(trimmed) {
			continue
		}
		if strings.HasSuffix(line, "  ") || strings.HasSuffix(line, "\\") {
			continue
		}
		lines[i] = line + "  "
	}
	return strings.Join(lines, "\n")
}

func standaloneLinkReference(line string) bool {
	return strings.HasPrefix(line, "**") && strings.HasSuffix(line, "**") && linkReferenceSuffix.FindString(line) != ""
}

func markdownListItem(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ")
}

func notionPageURL(attrs string) string {
	parts := notionURLAttributePattern.FindStringSubmatch(attrs)
	if len(parts) < 3 {
		return ""
	}
	if parts[1] != "" {
		return html.UnescapeString(strings.TrimSpace(parts[1]))
	}
	return html.UnescapeString(strings.TrimSpace(parts[2]))
}

func escapeMarkdownLinkLabel(label string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	return replacer.Replace(label)
}

func escapeMarkdownEmphasis(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`)
	return replacer.Replace(value)
}
