package notion

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	dashedUUIDRE = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	plainUUIDRE  = regexp.MustCompile(`(?i)[0-9a-f]{32}`)
)

func NormalizeID(value string) (string, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return "", fmt.Errorf("notion id is required")
	}
	if match := dashedUUIDRE.FindString(cleaned); match != "" {
		return strings.ToLower(match), nil
	}
	if match := plainUUIDRE.FindString(cleaned); match != "" {
		lower := strings.ToLower(match)
		return lower[0:8] + "-" + lower[8:12] + "-" + lower[12:16] + "-" + lower[16:20] + "-" + lower[20:32], nil
	}
	return "", fmt.Errorf("invalid Notion id or URL %q", value)
}
