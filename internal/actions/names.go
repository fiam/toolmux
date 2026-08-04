package actions

import (
	"strings"
	"unicode"
)

// CLIName converts provider and upstream identifiers to the kebab-case form
// used by the human CLI. Machine-facing action IDs and argument keys remain
// unchanged.
func CLIName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	runes := []rune(name)
	var out strings.Builder
	for i, current := range runes {
		if current == '_' || unicode.IsSpace(current) {
			writeCLINameSeparator(&out)
			continue
		}
		if current == '-' {
			writeCLINameSeparator(&out)
			continue
		}
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			return name
		}
		if unicode.IsUpper(current) && i > 0 {
			previous := runes[i-1]
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || (unicode.IsUpper(previous) && nextIsLower) {
				writeCLINameSeparator(&out)
			}
		}
		out.WriteRune(unicode.ToLower(current))
	}
	return strings.Trim(out.String(), "-")
}

func writeCLINameSeparator(out *strings.Builder) {
	if out.Len() > 0 && !strings.HasSuffix(out.String(), "-") {
		out.WriteByte('-')
	}
}
