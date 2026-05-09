package client

import (
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/notion"
)

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:                 string(notion.ProviderName),
		DisplayName:        "Notion",
		AuthMode:           "brokered_local_custody",
		Tree:               CommandTree(),
		Handlers:           ActionHandlers(),
		Diagnostics:        Diagnostics,
		DefaultPermissions: notion.DefaultCapabilities,
		BaseURLEnv:         "TOOLMUX_NOTION_API_URL",
		DefaultBaseURL:     DefaultBaseURL,
		APIVersionEnv:      "TOOLMUX_NOTION_VERSION",
		DefaultAPIVersion:  DefaultVersion,
	}
}
