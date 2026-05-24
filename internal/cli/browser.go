package cli

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var openURL = openBrowser

func openBrowser(rawURL string) error {
	command, args := openBrowserCommand(runtime.GOOS, os.Getenv("TOOLMUX_BROWSER"), os.Getenv("BROWSER"), rawURL)
	// #nosec G204,G702 -- the browser command is the platform default or an explicit user override.
	return exec.Command(command, args...).Start()
}

func openBrowserCommand(goosName, toolmuxBrowser, browser, rawURL string) (string, []string) {
	if override := strings.TrimSpace(toolmuxBrowser); override != "" {
		if goosName == "darwin" {
			return "open", []string{"-a", override, rawURL}
		}
		return browserCommand(override, rawURL)
	}
	if override := strings.TrimSpace(browser); override != "" {
		return browserCommand(override, rawURL)
	}

	switch goosName {
	case "darwin":
		return "open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		return "xdg-open", []string{rawURL}
	}
}

func browserCommand(browser, rawURL string) (string, []string) {
	fields := strings.Fields(browser)
	if len(fields) == 0 {
		return "xdg-open", []string{rawURL}
	}
	args := append([]string(nil), fields[1:]...)
	for i, arg := range args {
		if strings.Contains(arg, "%s") {
			args[i] = strings.ReplaceAll(arg, "%s", rawURL)
			return fields[0], args
		}
	}
	if strings.Contains(fields[0], "%s") {
		command := strings.ReplaceAll(fields[0], "%s", rawURL)
		return command, args
	}
	return fields[0], append(args, rawURL)
}
