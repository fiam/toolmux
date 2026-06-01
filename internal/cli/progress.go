package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
)

type connectUI struct {
	w            io.Writer
	output       *termenv.Output
	styles       connectStyles
	spinner      spinner.Spinner
	mu           sync.Mutex
	active       *connectProgressHandle
	interactive  bool
	clearOnWrite bool
}

type semanticTone string

const (
	toneInfo    semanticTone = "info"
	toneSuccess semanticTone = "success"
	toneWarning semanticTone = "warning"
)

type semanticPalette struct {
	info    string
	success string
	warning string
	muted   string
}

type connectStyles struct {
	info    lipgloss.Style
	success lipgloss.Style
	warning lipgloss.Style
	muted   lipgloss.Style
	spinner lipgloss.Style
}

func newConnectUI(cmd *cobra.Command, opts *options) *connectUI {
	stderr := cmd.ErrOrStderr()
	interactive := opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(stderr)
	terminal := termenv.NewOutput(stderr, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(interactive))
	color := interactive && colorEnabled(opts.color, interactive)
	palette := semanticPaletteFor(terminal, interactive)
	return &connectUI{
		w:            stderr,
		output:       terminal,
		styles:       newConnectStyles(color, palette),
		spinner:      spinnerStyle(terminal),
		interactive:  interactive,
		clearOnWrite: interactive,
	}
}

// spinnerStyle prefers the braille MiniDot animation but degrades to the ASCII
// line spinner on terminals that cannot render Unicode (dumb terminals or an
// Ascii color profile), matching the glyph fallback used by status badges.
func spinnerStyle(terminal *termenv.Output) spinner.Spinner {
	if terminal != nil && terminal.Profile == termenv.Ascii {
		return spinner.Line
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return spinner.Line
	}
	return spinner.MiniDot
}

func (ui *connectUI) Start(message string) actions.ProgressHandle {
	if !ui.interactive {
		return noopCLIProgressHandle{}
	}
	handle := &connectProgressHandle{
		ui:      ui,
		message: strings.TrimSpace(message),
		done:    make(chan struct{}),
		start:   time.Now(),
	}
	ui.mu.Lock()
	ui.stopLocked()
	ui.active = handle
	ui.mu.Unlock()
	go handle.run()
	return handle
}

func (ui *connectUI) Status(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneInfo, "i", message)
}

func (ui *connectUI) Warn(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneWarning, "!", message)
}

func (ui *connectUI) Done(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneSuccess, "+", message)
}

func (ui *connectUI) status(format string, args ...any) {
	ui.Status(fmt.Sprintf(format, args...))
}

func (ui *connectUI) warn(format string, args ...any) {
	ui.Warn(fmt.Sprintf(format, args...))
}

func (ui *connectUI) writeLine(tone semanticTone, marker, message string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.stopLocked()
	fmt.Fprintf(ui.w, "%s %s\n", ui.marker(tone, marker), strings.TrimSpace(message))
}

func (ui *connectUI) stopLocked() {
	if ui.active != nil {
		ui.active.close()
		ui.active = nil
	}
	ui.clearLineLocked()
}

func (ui *connectUI) clearLineLocked() {
	if !ui.clearOnWrite {
		return
	}
	fmt.Fprint(ui.w, "\r")
	if ui.output != nil {
		ui.output.ClearLine()
	}
}

func (ui *connectUI) marker(tone semanticTone, value string) string {
	switch tone {
	case toneInfo:
		return ui.styles.info.Render(value)
	case toneSuccess:
		return ui.styles.success.Render(value)
	case toneWarning:
		return ui.styles.warning.Render(value)
	default:
		return value
	}
}

type connectProgressHandle struct {
	ui      *connectUI
	message string
	frame   int
	start   time.Time
	done    chan struct{}
	once    sync.Once
}

func (handle *connectProgressHandle) run() {
	handle.render()
	interval := handle.ui.spinner.FPS
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-handle.done:
			return
		case <-ticker.C:
			handle.ui.mu.Lock()
			if handle.ui.active != handle {
				handle.ui.mu.Unlock()
				return
			}
			handle.frame++
			handle.renderLocked()
			handle.ui.mu.Unlock()
		}
	}
}

func (handle *connectProgressHandle) Update(message string) {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		return
	}
	handle.message = strings.TrimSpace(message)
	handle.renderLocked()
}

func (handle *connectProgressHandle) Stop() {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active == handle {
		handle.close()
		handle.ui.active = nil
		handle.ui.clearLineLocked()
		return
	}
	handle.close()
}

func (handle *connectProgressHandle) Warn(message string) {
	handle.finish(toneWarning, "!", message)
}

func (handle *connectProgressHandle) Done(message string) {
	handle.finish(toneSuccess, "+", message)
}

func (handle *connectProgressHandle) finish(tone semanticTone, marker, message string) {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		handle.close()
		return
	}
	handle.close()
	handle.ui.active = nil
	handle.ui.clearLineLocked()
	fmt.Fprintf(handle.ui.w, "%s %s\n", handle.ui.marker(tone, marker), strings.TrimSpace(message))
}

func (handle *connectProgressHandle) render() {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		return
	}
	handle.renderLocked()
}

func (handle *connectProgressHandle) renderLocked() {
	handle.ui.clearLineLocked()
	frames := handle.ui.spinner.Frames
	frame := "-"
	if len(frames) > 0 {
		frame = frames[handle.frame%len(frames)]
	}
	line := handle.ui.styles.muted.Render(handle.message)
	if elapsed := handle.elapsed(); elapsed != "" {
		line += " " + handle.ui.styles.muted.Render("("+elapsed+")")
	}
	fmt.Fprintf(handle.ui.w, "%s %s", handle.ui.styles.spinner.Render(frame), line)
}

// elapsed renders a compact, muted runtime suffix once the spinner has been
// visible long enough to be worth showing, so slow operations feel honest.
func (handle *connectProgressHandle) elapsed() string {
	if handle.start.IsZero() {
		return ""
	}
	d := time.Since(handle.start)
	if d < time.Second {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (handle *connectProgressHandle) close() {
	handle.once.Do(func() {
		close(handle.done)
	})
}

type noopCLIProgressHandle struct{}

func (noopCLIProgressHandle) Update(string) {}
func (noopCLIProgressHandle) Stop()         {}
func (noopCLIProgressHandle) Warn(string)   {}
func (noopCLIProgressHandle) Done(string)   {}

func newConnectStyles(color bool, palette semanticPalette) connectStyles {
	if !color {
		style := lipgloss.NewStyle()
		return connectStyles{
			info:    style,
			success: style,
			warning: style,
			muted:   style,
			spinner: style,
		}
	}
	return connectStyles{
		info:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.info)),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.success)),
		warning: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.warning)),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.muted)),
		spinner: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.info)),
	}
}

func semanticPaletteFor(output *termenv.Output, interactive bool) semanticPalette {
	if interactive && output != nil && !output.HasDarkBackground() {
		return semanticPalette{
			info:    "#0969da",
			success: "#1a7f37",
			warning: "#9a6700",
			muted:   "#6e7781",
		}
	}
	return semanticPalette{
		info:    "#7dd3fc",
		success: "#86efac",
		warning: "#facc15",
		muted:   "#8ea0b8",
	}
}
