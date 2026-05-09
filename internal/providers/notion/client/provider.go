package client

import (
	"github.com/fiam/supacli/internal/providers"
	"github.com/fiam/supacli/internal/providers/notion"
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
		BaseURLEnv:         "SUPACLI_NOTION_API_URL",
		DefaultBaseURL:     DefaultBaseURL,
		APIVersionEnv:      "SUPACLI_NOTION_VERSION",
		DefaultAPIVersion:  DefaultVersion,
	}
}
