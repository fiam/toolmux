package notion

import "github.com/fiam/toolmux/internal/actions"

const ProviderName actions.ProviderName = "notion"

const DefaultVersion = "2026-03-11"

const (
	CapabilityReadContent   = "read_content"
	CapabilityInsertContent = "insert_content"
	CapabilityUpdateContent = "update_content"
)

func DefaultCapabilities() []string {
	return []string{CapabilityReadContent, CapabilityInsertContent, CapabilityUpdateContent}
}
