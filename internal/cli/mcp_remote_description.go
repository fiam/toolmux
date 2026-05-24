package cli

import (
	_ "embed"
	"strings"

	"github.com/spf13/cobra"
)

func mcpRemoteDisplayDescription(cmd *cobra.Command, opts *options, description string, full bool) string {
	if !full && mcpRemoteCompactDescriptions(cmd, opts) {
		return mcpRemoteCompactDescription(description)
	}
	return description
}

func mcpRemoteCompactDescriptions(cmd *cobra.Command, opts *options) bool {
	return opts != nil && interactiveCommand(cmd, opts)
}

func mcpRemoteCompactDescription(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}
	lines := strings.Split(description, "\n")
	for index, line := range lines {
		if mcpRemoteDescriptionHeading(line) {
			continue
		}
		line = mcpRemoteCleanDescriptionLine(line)
		if line == "" {
			continue
		}
		if mcpRemoteGenericDescriptionLine(line) {
			continue
		}
		if strings.HasSuffix(line, ":") {
			if combined := mcpRemoteCompactColonDescription(line, lines[index+1:]); combined != "" {
				return truncateMCPRemoteDescription(combined, mcpRemoteCompactDescriptionLimit)
			}
			line = strings.TrimSpace(strings.TrimSuffix(line, ":"))
			if mcpRemoteIncompleteDescriptionLine(line) {
				continue
			}
		}
		return truncateMCPRemoteDescription(line, mcpRemoteCompactDescriptionLimit)
	}
	return truncateMCPRemoteDescription(strings.Join(strings.Fields(description), " "), mcpRemoteCompactDescriptionLimit)
}

func mcpRemoteDescriptionHeading(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "#")
}

func mcpRemoteCleanDescriptionLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
		return ""
	}
	for strings.HasPrefix(line, ">") {
		line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	}
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	line = mcpRemoteStripListMarker(line)
	line = strings.TrimSpace(line)
	for _, checkbox := range []string{"[ ] ", "[x] ", "[X] "} {
		line = strings.TrimPrefix(line, checkbox)
	}
	line = mcpRemoteMarkdownLinkPattern.ReplaceAllString(line, "$1")
	replacer := strings.NewReplacer("`", "", "**", "", "__", "")
	line = replacer.Replace(line)
	return strings.Join(strings.Fields(line), " ")
}

func mcpRemoteStripListMarker(line string) string {
	line = strings.TrimSpace(line)
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		return strings.TrimSpace(line[1:])
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			if i > 0 && (r == '.' || r == ')') && len(line) > i+1 && (line[i+1] == ' ' || line[i+1] == '\t') {
				return strings.TrimSpace(line[i+1:])
			}
			break
		}
	}
	return line
}

func mcpRemoteDescriptionListItem(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		return true
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			return i > 0 && (r == '.' || r == ')') && len(line) > i+1 && (line[i+1] == ' ' || line[i+1] == '\t')
		}
	}
	return false
}

func mcpRemoteGenericDescriptionLine(line string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(line)), ":. ")
	switch normalized {
	case "overview", "summary", "description", "details", "usage", "examples", "example", "notes", "note", "instructions":
		return true
	default:
		return false
	}
}

func mcpRemoteIncompleteDescriptionLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return true
	}
	switch strings.ToLower(fields[len(fields)-1]) {
	case "for", "from", "in", "into", "of", "on", "over", "to", "using", "with":
		return true
	default:
		return len(fields) <= 3
	}
}

func mcpRemoteCompactColonDescription(prefix string, following []string) string {
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, ":"))
	var bullets []string
	for _, line := range following {
		if strings.TrimSpace(line) == "" {
			if len(bullets) == 0 {
				continue
			}
			break
		}
		if !mcpRemoteDescriptionListItem(line) {
			if len(bullets) == 0 && (mcpRemoteDescriptionHeading(line) || mcpRemoteGenericDescriptionLine(mcpRemoteCleanDescriptionLine(line))) {
				continue
			}
			break
		}
		bullet := mcpRemoteCleanDescriptionLine(line)
		if bullet == "" {
			continue
		}
		bullets = append(bullets, bullet)
		if len(bullets) == 3 {
			break
		}
	}
	if len(bullets) == 0 {
		return ""
	}
	description := prefix + " " + strings.Join(bullets, ", ")
	if !strings.ContainsAny(description[len(description)-1:], ".!?") {
		description += "."
	}
	return description
}

func truncateMCPRemoteDescription(description string, limit int) string {
	if limit <= 0 || len(description) <= limit {
		return description
	}
	cut := strings.LastIndexAny(description[:limit+1], " \t")
	if cut <= 0 {
		cut = limit
	}
	return strings.TrimSpace(description[:cut]) + "..."
}
