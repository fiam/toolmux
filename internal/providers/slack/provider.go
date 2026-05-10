package slack

import "github.com/fiam/toolmux/internal/actions"

const ProviderName actions.ProviderName = "slack"

const (
	CapabilityReadConversations = "channels:read"
	CapabilityReadGroups        = "groups:read"
	CapabilityReadIMs           = "im:read"
	CapabilityReadMPIMs         = "mpim:read"
	CapabilitySearch            = "search:read"
	CapabilityWriteChat         = "chat:write"
)

func DefaultCapabilities() []string {
	return []string{
		CapabilityReadConversations,
		CapabilityReadGroups,
		CapabilityReadIMs,
		CapabilityReadMPIMs,
		CapabilitySearch,
		CapabilityWriteChat,
	}
}
