package google

import "github.com/fiam/supacli/internal/providers"

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:          "google",
		DisplayName: "Google",
		AuthMode:    "native_pkce",
		Aliases:     []string{"google-docs", "google-drive", "gmail"},
	}
}
