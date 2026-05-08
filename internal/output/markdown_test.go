package output

import (
	"strings"
	"testing"
)

func TestRenderMarkdownPlainKeepsReadableText(t *testing.T) {
	rendered, err := RenderMarkdown("# Roadmap\n\n- Ship Toolmux", MarkdownOptions{
		Width: 80,
		Theme: MarkdownPlain,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "Roadmap") || !strings.Contains(rendered, "Ship Toolmux") {
		t.Fatalf("rendered markdown lost content: %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("plain markdown render contains ANSI escape sequence: %q", rendered)
	}
}

func TestPrepareReadableMarkdownAnnotatesLinksAndPreservesLines(t *testing.T) {
	source := PrepareReadableMarkdown("# Work Diary\n\n[April 2026](https://notion.so/april)\n[May 2026](https://notion.so/may)")
	if !strings.Contains(source, "- **April 2026 [1]**") {
		t.Fatalf("prepared markdown does not annotate first link: %q", source)
	}
	if !strings.Contains(source, "1. April 2026 - https://notion.so/april") {
		t.Fatalf("prepared markdown does not include visible link destination: %q", source)
	}
	if !strings.Contains(source, "- **April 2026 [1]**\n- **May 2026 [2]**") {
		t.Fatalf("prepared markdown does not preserve adjacent link line break: %q", source)
	}

	rendered, err := RenderMarkdown(source, MarkdownOptions{Width: 100, Theme: MarkdownPlain})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "April 2026 May 2026") {
		t.Fatalf("rendered markdown collapsed adjacent link lines: %q", rendered)
	}
	if strings.Contains(rendered, "April 2026 [1] May 2026 [2]") {
		t.Fatalf("rendered markdown collapsed adjacent link references: %q", rendered)
	}
	if strings.Contains(rendered, "April 2026 [1] https://notion.so/april") {
		t.Fatalf("rendered markdown includes verbose inline URL: %q", rendered)
	}
	if !strings.Contains(rendered, "https://notion.so/april") || !strings.Contains(rendered, "https://notion.so/may") {
		t.Fatalf("rendered markdown lost visible link destinations: %q", rendered)
	}
}

func TestPrepareReadableMarkdownNormalizesNotionPageLinks(t *testing.T) {
	source := PrepareReadableMarkdown(`<page url="https://www.notion.so/april">April 2026</page>
<page url="https://www.notion.so/may">May 2026</page>
<empty-block/>`)
	if !strings.Contains(source, "- **April 2026 [1]**") {
		t.Fatalf("prepared markdown does not convert Notion page link: %q", source)
	}
	if !strings.Contains(source, "1. April 2026 - https://www.notion.so/april") {
		t.Fatalf("prepared markdown does not include Notion page link destination: %q", source)
	}
	if !strings.Contains(source, "- **April 2026 [1]**\n- **May 2026 [2]**") {
		t.Fatalf("prepared markdown does not preserve Notion page link line break: %q", source)
	}
	if strings.Contains(source, "<page") || strings.Contains(source, "<empty-block") {
		t.Fatalf("prepared markdown still contains Notion enhanced tags: %q", source)
	}

	rendered, err := RenderMarkdown(source, MarkdownOptions{Width: 100, Theme: MarkdownPlain})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "April 2026 May 2026") {
		t.Fatalf("rendered markdown collapsed Notion page links: %q", rendered)
	}
	if strings.Contains(rendered, "April 2026 [1] May 2026 [2]") {
		t.Fatalf("rendered markdown collapsed adjacent Notion page link references: %q", rendered)
	}
	if strings.Contains(rendered, "April 2026 [1] https://www.notion.so/april") {
		t.Fatalf("rendered markdown includes verbose inline Notion URL: %q", rendered)
	}
	if !strings.Contains(rendered, "April 2026") || !strings.Contains(rendered, "https://www.notion.so/april") {
		t.Fatalf("rendered markdown lost Notion page link detail: %q", rendered)
	}
}

func TestExtractMarkdownLinksNormalizesNotionPageLinks(t *testing.T) {
	links := ExtractMarkdownLinks(`<page url="https://www.notion.so/Roadmap-11111111111141118111111111111111">Roadmap</page>
[External](https://example.com/docs)`)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %#v", links)
	}
	if links[0].Label != "Roadmap" || links[0].URL != "https://www.notion.so/Roadmap-11111111111141118111111111111111" {
		t.Fatalf("unexpected Notion link: %#v", links[0])
	}
	if links[1].Label != "External" || links[1].URL != "https://example.com/docs" {
		t.Fatalf("unexpected external link: %#v", links[1])
	}
}
