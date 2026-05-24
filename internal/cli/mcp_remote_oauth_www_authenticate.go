package cli

import (
	"strings"
)

func mcpRemoteWWWAuthenticateResourceMetadata(values []string) []string {
	var urls []string
	for _, value := range values {
		remaining := value
		for {
			index := strings.Index(strings.ToLower(remaining), "resource_metadata")
			if index < 0 {
				break
			}
			remaining = remaining[index+len("resource_metadata"):]
			remaining = strings.TrimLeft(remaining, " \t")
			if !strings.HasPrefix(remaining, "=") {
				continue
			}
			remaining = strings.TrimLeft(remaining[1:], " \t")
			metadataURL, rest := readMCPRemoteWWWAuthenticateValue(remaining)
			if metadataURL != "" {
				urls = append(urls, metadataURL)
			}
			if rest == "" || rest == remaining {
				break
			}
			remaining = rest
		}
	}
	return urls
}

func readMCPRemoteWWWAuthenticateValue(value string) (string, string) {
	if strings.HasPrefix(value, `"`) {
		var out strings.Builder
		escaped := false
		for i, r := range value[1:] {
			switch {
			case escaped:
				out.WriteRune(r)
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				return out.String(), value[i+2:]
			default:
				out.WriteRune(r)
			}
		}
		return out.String(), ""
	}
	end := len(value)
	for i, r := range value {
		if r == ',' || r == ' ' || r == '\t' {
			end = i
			break
		}
	}
	return strings.TrimSpace(value[:end]), value[end:]
}
