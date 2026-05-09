package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTablePlainHasHeadersAndNoANSI(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	RenderTable(&out, Options{Color: false, Width: 160}, Table{
		Headers: []string{"Provider", "Status"},
		Rows: [][]string{
			{"notion", StatusBadge(Options{Color: false}, "connected")},
		},
	})
	rendered := out.String()
	if !strings.Contains(rendered, "Provider") || !strings.Contains(rendered, "notion") {
		t.Fatalf("rendered table lost content: %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("plain table render contains ANSI escape sequence: %q", rendered)
	}
	if width := maxLineWidth(rendered); width >= 80 {
		t.Fatalf("expected compact table width, got %d columns in %q", width, rendered)
	}
}

func TestStatusBadgeColorUsesANSI(t *testing.T) {
	t.Parallel()
	badge := StatusBadge(Options{Color: true, DarkBackground: true}, "ok")
	if !strings.Contains(badge, "\x1b[") {
		t.Fatalf("expected colored badge to contain ANSI escape sequence: %q", badge)
	}
}

func maxLineWidth(value string) int {
	maximum := 0
	for line := range strings.SplitSeq(strings.TrimRight(value, "\n"), "\n") {
		if len(line) > maximum {
			maximum = len(line)
		}
	}
	return maximum
}
