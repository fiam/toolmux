package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/output"
)

func commandContext(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func interactiveCommand(cmd *cobra.Command, opts *options) bool {
	return opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(cmd.ErrOrStderr()) && isInputTerminal(cmd.InOrStdin())
}

func markdownForOutput(w io.Writer, opts *options, source string) string {
	if !isTerminal(w) || os.Getenv("TERM") == "dumb" {
		return source
	}
	width := terminalWidth(w)
	theme := output.MarkdownDark
	if !colorEnabled(opts.color, true) {
		theme = output.MarkdownPlain
	} else {
		terminal := termenv.NewOutput(w, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(true))
		if !terminal.HasDarkBackground() {
			theme = output.MarkdownLight
		}
	}
	rendered, err := output.RenderMarkdown(source, output.MarkdownOptions{
		Width: width,
		Theme: theme,
	})
	if err != nil {
		return source
	}
	return strings.TrimRight(rendered, "\n")
}

func humanOutputOptions(cmd *cobra.Command, opts *options) output.Options {
	w := cmd.OutOrStdout()
	tty := isTerminal(w)
	terminal := termenv.NewOutput(w, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(tty))
	color := colorEnabled(opts.color, tty)
	darkBackground := true
	if tty {
		darkBackground = terminal.HasDarkBackground()
	}
	return output.Options{
		Color:          color,
		DarkBackground: darkBackground,
		Width:          terminalWidth(w),
	}
}

func writePossiblyPaged(cmd *cobra.Command, opts *options, content string) error {
	text := strings.TrimRight(content, "\n") + "\n"
	if shouldPage(cmd.OutOrStdout(), opts, text) {
		pager, ok := pagerCommand()
		if ok {
			return runPager(cmd, pager, text)
		}
	}
	fmt.Fprint(cmd.OutOrStdout(), text)
	return nil
}

func shouldPage(w io.Writer, opts *options, content string) bool {
	switch strings.ToLower(strings.TrimSpace(opts.pager)) {
	case "never":
		return false
	case "always":
		return isTerminal(w)
	default:
		return isTerminal(w) && lineCount(content) > terminalHeight(w)-2
	}
}

func pagerCommand() (string, bool) {
	if pager := strings.TrimSpace(os.Getenv("PAGER")); pager != "" {
		return pager, true
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less -R", true
	}
	return "", false
}

func runPager(cmd *cobra.Command, pager, content string) error {
	name, args := pagerShellCommand(pager)
	// #nosec G204 -- the pager is an explicit user-controlled terminal command.
	process := exec.CommandContext(cmd.Context(), name, args...)
	process.Stdin = strings.NewReader(content)
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	return process.Run()
}

func pagerShellCommand(pager string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", pager}
	}
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-c", pager}
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func writeActionResult(cmd *cobra.Command, opts *options, execCtx actions.Context, result any) error {
	for {
		if err := writeActionResultOnce(cmd, opts, result); err != nil {
			return err
		}
		follower, ok := result.(actions.FollowRenderable)
		if !ok {
			return nil
		}
		next, keepGoing, err := follower.Follow(execCtx)
		if err != nil {
			return err
		}
		if !keepGoing || next == nil {
			return nil
		}
		if opts.output == "table" {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		result = next
	}
}

func writeActionResultOnce(cmd *cobra.Command, opts *options, result any) error {
	if result == nil {
		return nil
	}
	switch opts.output {
	case "json", "yaml":
		return writeValue(cmd, opts, result, nil)
	case "table":
		if opener, ok := result.(actions.BrowserOpenRenderable); ok && opener.BrowserURL() != "" && !opener.BrowserURLOnly() {
			if err := openURL(opener.BrowserURL()); err != nil {
				return fmt.Errorf("open %q: %w", opener.BrowserURL(), err)
			}
		}
		if markdown, ok := result.(actions.MarkdownRenderable); ok {
			source := markdown.MarkdownSource()
			rendered := markdownForOutput(cmd.OutOrStdout(), opts, source)
			if truncated, unknown := markdown.MarkdownTruncated(); truncated {
				rendered += fmt.Sprintf("\n\n%s\n", output.ToneText(humanOutputOptions(cmd, opts), output.ToneWarning, fmt.Sprintf("truncated: %d unknown blocks", unknown)))
			}
			return writePossiblyPaged(cmd, opts, rendered)
		}
		if text, ok := result.(actions.TextRenderable); ok {
			return writePossiblyPaged(cmd, opts, text.Text())
		}
		if table, ok := result.(actions.TableRenderable); ok {
			output.RenderTable(cmd.OutOrStdout(), humanOutputOptions(cmd, opts), table.Table(humanOutputOptions(cmd, opts)))
			return nil
		}
		return writeValue(cmd, opts, result, nil)
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
	}
}

func writeValue(cmd *cobra.Command, opts *options, value any, table func(io.Writer)) error {
	switch opts.output {
	case "json":
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	case "yaml":
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		defer encoder.Close()
		return encoder.Encode(value)
	case "table":
		if table != nil {
			table(cmd.OutOrStdout())
			return nil
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
	}
}
