package client

import (
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/slack"
)

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:                 string(slack.ProviderName),
		DisplayName:        "Slack",
		AuthMode:           "brokered_local_custody",
		Tree:               CommandTree(),
		Handlers:           ActionHandlers(),
		Diagnostics:        Diagnostics,
		DefaultPermissions: slack.DefaultCapabilities,
		BaseURLEnv:         "TOOLMUX_SLACK_API_URL",
		DefaultBaseURL:     DefaultBaseURL,
	}
}
