package cli

import (
	"io"
	"os"
	"strings"

	"github.com/muesli/termenv"
	"golang.org/x/term"
)

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func isInputTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func terminalWidth(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 100
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 100
	}
	if width < 40 {
		return 40
	}
	return width
}

func terminalHeight(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 24
	}
	_, height, err := term.GetSize(int(file.Fd()))
	if err != nil || height <= 0 {
		return 24
	}
	if height < 10 {
		return 10
	}
	return height
}

func colorAllowed() bool {
	if termenv.EnvNoColor() {
		return false
	}
	if colorForced() {
		return true
	}
	return os.Getenv("CLICOLOR") != "0" && os.Getenv("TERM") != "dumb"
}

func colorEnabled(policy string, tty bool) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "always":
		return true
	case "never":
		return false
	default:
		return colorAllowed() && (tty || colorForced())
	}
}

func colorForced() bool {
	force := os.Getenv("CLICOLOR_FORCE")
	return force != "" && force != "0"
}
