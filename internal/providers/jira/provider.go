package jira

import "github.com/fiam/supacli/internal/providers"

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:          "jira",
		DisplayName: "Jira",
		AuthMode:    "brokered_local_custody",
	}
}
