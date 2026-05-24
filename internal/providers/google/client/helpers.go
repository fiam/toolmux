package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
)

func timeout(inv actions.Invocation) time.Duration {
	seconds := inv.Int("timeout-seconds")
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func account(inv actions.Invocation) string {
	if value := strings.TrimSpace(inv.String("account")); value != "" {
		return value
	}
	return defaultAccount
}

func requiredString(inv actions.Invocation, name string) (string, error) {
	value := strings.TrimSpace(inv.String(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func driveCopySource(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("file"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	switch {
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the Google Drive file as either --file or a positional argument, not both")
	case flagValue != "":
		return flagValue, nil
	case argValue != "":
		return argValue, nil
	default:
		return "", fmt.Errorf("google drive file ID or URL is required")
	}
}

func googleDriveFileID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("google drive file ID or URL is required")
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
			return id, nil
		}
		segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		for i, segment := range segments {
			if segment != "d" && segment != "folders" {
				continue
			}
			if i+1 >= len(segments) {
				break
			}
			id, err := url.PathUnescape(segments[i+1])
			if err != nil {
				return "", err
			}
			if id = strings.TrimSpace(id); id != "" {
				return id, nil
			}
		}
		return "", fmt.Errorf("could not find a Google Drive file ID in %q", value)
	}
	if strings.ContainsAny(value, "/?#") {
		return "", fmt.Errorf("could not parse %q as a Google Drive URL; pass the raw file ID instead", value)
	}
	return value, nil
}

func hasAnySecretFlag(inv actions.Invocation, name string) bool {
	return strings.TrimSpace(inv.String(name)) != "" ||
		strings.TrimSpace(inv.String(name+"-env")) != "" ||
		strings.TrimSpace(inv.String(name+"-file")) != ""
}

func mergeExtra(base, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range overlay {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}
