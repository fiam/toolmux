package slack

import "github.com/fiam/supacli/internal/providers"

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:          "slack",
		DisplayName: "Slack",
		AuthMode:    "native_pkce",
	}
}
