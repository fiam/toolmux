package cli

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
)

type mcpToolSelector struct {
	includeGlobs []string
	includeRegex []*regexp.Regexp
	excludeGlobs []string
	excludeRegex []*regexp.Regexp
	includeAll   bool
	profile      string
}

func newMCPToolSelector(selection mcpToolSelection) (mcpToolSelector, error) {
	selector := mcpToolSelector{
		includeGlobs: compactStrings(selection.Tools),
		excludeGlobs: compactStrings(selection.ExcludeTools),
		profile:      strings.TrimSpace(selection.Profile),
	}
	for _, pattern := range append(selector.includeGlobs, selector.excludeGlobs...) {
		if _, err := path.Match(pattern, "toolmux.test"); err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP tool glob %q: %w", pattern, err)
		}
	}
	for _, pattern := range compactStrings(selection.ToolRegex) {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP tool regex %q: %w", pattern, err)
		}
		selector.includeRegex = append(selector.includeRegex, compiled)
	}
	for _, pattern := range compactStrings(selection.ExcludeToolRegex) {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP exclude regex %q: %w", pattern, err)
		}
		selector.excludeRegex = append(selector.excludeRegex, compiled)
	}
	selector.includeAll = len(selector.includeGlobs) == 0 && len(selector.includeRegex) == 0
	return selector, nil
}

func (selector mcpToolSelector) matches(spec actions.Spec) bool {
	targets := mcpToolTargets(spec)
	if selector.excluded(targets) {
		return false
	}
	if selector.includeAll {
		return true
	}
	return selector.included(targets)
}

func (selector mcpToolSelector) included(targets []string) bool {
	for _, target := range targets {
		for _, glob := range selector.includeGlobs {
			if matched, _ := path.Match(glob, target); matched {
				return true
			}
		}
		for _, regex := range selector.includeRegex {
			if regex.MatchString(target) {
				return true
			}
		}
	}
	return false
}

func (selector mcpToolSelector) excluded(targets []string) bool {
	for _, target := range targets {
		for _, glob := range selector.excludeGlobs {
			if matched, _ := path.Match(glob, target); matched {
				return true
			}
		}
		for _, regex := range selector.excludeRegex {
			if regex.MatchString(target) {
				return true
			}
		}
	}
	return false
}

func mcpToolTargets(spec actions.Spec) []string {
	pathWithDots := strings.Join(spec.Path, ".")
	pathWithSpaces := strings.Join(spec.Path, " ")
	targets := []string{spec.ID}
	if pathWithDots != "" && pathWithDots != spec.ID {
		targets = append(targets, pathWithDots)
	}
	if pathWithSpaces != "" {
		targets = append(targets, pathWithSpaces)
	}
	return targets
}

func mcpToolSelectionArgs(selection mcpToolSelection) []string {
	var args []string
	if profile := strings.TrimSpace(selection.Profile); profile != "" {
		args = append(args, "--mcp-profile", profile)
	}
	for _, value := range compactStrings(selection.Tools) {
		args = append(args, "--tool", value)
	}
	for _, value := range compactStrings(selection.ToolRegex) {
		args = append(args, "--tool-regex", value)
	}
	for _, value := range compactStrings(selection.ExcludeTools) {
		args = append(args, "--exclude-tool", value)
	}
	for _, value := range compactStrings(selection.ExcludeToolRegex) {
		args = append(args, "--exclude-tool-regex", value)
	}
	return args
}

func compactStrings(values []string) []string {
	compact := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			compact = append(compact, value)
		}
	}
	return compact
}
