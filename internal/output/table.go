package output

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	liptable "charm.land/lipgloss/v2/table"
)

type Options struct {
	Color          bool
	DarkBackground bool
	Width          int
}

type Tone string

const (
	ToneDefault Tone = "default"
	ToneInfo    Tone = "info"
	ToneSuccess Tone = "success"
	ToneWarning Tone = "warning"
	ToneDanger  Tone = "danger"
	ToneMuted   Tone = "muted"
)

type Align int

const (
	AlignLeft Align = iota
	AlignRight
)

type Table struct {
	Headers   []string
	Rows      [][]string
	Empty     string
	FullWidth bool
	// Align optionally sets per-column alignment (indexed by column).
	// Columns without an entry default to AlignLeft.
	Align []Align
}

// RightAlign builds an alignment slice of the given width with the listed
// column indices right-aligned and the rest left-aligned.
func RightAlign(columns int, right ...int) []Align {
	align := make([]Align, columns)
	for _, col := range right {
		if col >= 0 && col < columns {
			align[col] = AlignRight
		}
	}
	return align
}

type theme struct {
	cell    lipgloss.Style
	header  lipgloss.Style
	border  lipgloss.Style
	info    lipgloss.Style
	success lipgloss.Style
	warning lipgloss.Style
	danger  lipgloss.Style
	muted   lipgloss.Style
}

func RenderTable(w io.Writer, opts Options, model Table) {
	if len(model.Rows) == 0 {
		empty := model.Empty
		if empty == "" {
			empty = "no results"
		}
		fmt.Fprintln(w, ToneText(opts, ToneMuted, empty))
		return
	}
	t := newTheme(opts)
	table := liptable.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(true).
		BorderStyle(t.border).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := t.cell
			if row == liptable.HeaderRow {
				style = t.header
			}
			if columnAlign(model.Align, col) == AlignRight {
				style = style.Align(lipgloss.Right)
			}
			return style
		}).
		Headers(model.Headers...).
		Rows(model.Rows...)
	if model.FullWidth && opts.Width > 0 {
		table.Width(opts.Width)
	}
	fmt.Fprintln(w, strings.TrimRight(table.String(), "\n"))
}

func StatusBadge(opts Options, status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	tone := ToneInfo
	switch normalized {
	case "ok", "active", "allowed", "complete", "connected", "synced":
		tone = ToneSuccess
	case "warn", "warning", "pending", "disconnected", "needs_auth", "not_synced", "trashed", "alias_required", "unavailable":
		tone = ToneWarning
	case "fail", "failed", "error", "denied":
		tone = ToneDanger
	case "":
		return ToneText(opts, ToneMuted, "-")
	}
	label := strings.ReplaceAll(normalized, "_", " ")
	if glyph := toneGlyph(tone); glyph != "" {
		label = glyph + " " + label
	}
	return ToneText(opts, tone, label)
}

// toneGlyph returns a single-width leading symbol for a tone so status cells
// stay legible without color (e.g. NO_COLOR or piped output).
func toneGlyph(tone Tone) string {
	switch tone {
	case ToneSuccess:
		return "✓"
	case ToneWarning:
		return "⚠"
	case ToneDanger:
		return "✗"
	case ToneInfo:
		return "•"
	case ToneMuted:
		return "○"
	default:
		return ""
	}
}

func columnAlign(align []Align, col int) Align {
	if col < 0 || col >= len(align) {
		return AlignLeft
	}
	return align[col]
}

func ToneText(opts Options, tone Tone, value string) string {
	if value == "" {
		return ""
	}
	if !opts.Color {
		return value
	}
	t := newTheme(opts)
	switch tone {
	case ToneInfo:
		return t.info.Render(value)
	case ToneSuccess:
		return t.success.Render(value)
	case ToneWarning:
		return t.warning.Render(value)
	case ToneDanger:
		return t.danger.Render(value)
	case ToneMuted:
		return t.muted.Render(value)
	default:
		return value
	}
}

func JoinList(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func Value(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func newTheme(opts Options) theme {
	if !opts.Color {
		style := lipgloss.NewStyle().Padding(0, 1)
		return theme{
			cell:    style,
			header:  style,
			border:  lipgloss.NewStyle(),
			info:    lipgloss.NewStyle(),
			success: lipgloss.NewStyle(),
			warning: lipgloss.NewStyle(),
			danger:  lipgloss.NewStyle(),
			muted:   lipgloss.NewStyle(),
		}
	}
	palette := darkPalette()
	if !opts.DarkBackground {
		palette = lightPalette()
	}
	return theme{
		// Cells carry no base foreground: plain cells use the terminal default
		// foreground and tone-colored cells emit a single SGR sequence instead of
		// nesting one inside another.
		cell:    lipgloss.NewStyle().Padding(0, 1),
		header:  lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(lipgloss.Color(palette.header)),
		border:  lipgloss.NewStyle().Foreground(lipgloss.Color(palette.border)),
		info:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.info)),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.success)),
		warning: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.warning)),
		danger:  lipgloss.NewStyle().Foreground(lipgloss.Color(palette.danger)),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.muted)),
	}
}

type palette struct {
	header  string
	border  string
	info    string
	success string
	warning string
	danger  string
	muted   string
}

func darkPalette() palette {
	return palette{
		header:  "#cbd6e6",
		border:  "#334155",
		info:    "#7dd3fc",
		success: "#86efac",
		warning: "#facc15",
		danger:  "#fca5a5",
		muted:   "#8ea0b8",
	}
}

func lightPalette() palette {
	return palette{
		header:  "#57606a",
		border:  "#d0d7de",
		info:    "#0969da",
		success: "#1a7f37",
		warning: "#9a6700",
		danger:  "#cf222e",
		muted:   "#6e7781",
	}
}
