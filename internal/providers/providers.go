package providers

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/fiam/toolmux/internal/actions"
)

type Provider struct {
	ID                 string
	DisplayName        string
	AuthMode           string
	Aliases            []string
	Tree               actions.Spec
	Handlers           map[string]actions.Handler
	Diagnostics        actions.DiagnosticsFunc
	DefaultPermissions func() []string
	BaseURLEnv         string
	DefaultBaseURL     string
	APIVersionEnv      string
	DefaultAPIVersion  string
}

var registry = struct {
	sync.RWMutex
	byID  map[string]Provider
	alias map[string]string
}{
	byID:  map[string]Provider{},
	alias: map[string]string{},
}

func Register(provider Provider) {
	provider = cloneProvider(provider)
	provider.ID = strings.TrimSpace(provider.ID)
	provider.DisplayName = strings.TrimSpace(provider.DisplayName)
	provider.AuthMode = strings.TrimSpace(provider.AuthMode)
	for i, alias := range provider.Aliases {
		provider.Aliases[i] = strings.TrimSpace(alias)
	}
	provider.Aliases = slices.DeleteFunc(provider.Aliases, func(alias string) bool {
		return alias == "" || alias == provider.ID
	})
	if provider.ID == "" {
		panic("provider id is required")
	}
	if provider.DisplayName == "" {
		panic("provider display name is required for " + provider.ID)
	}
	registry.Lock()
	defer registry.Unlock()
	if _, ok := registry.byID[provider.ID]; ok {
		panic("provider already registered: " + provider.ID)
	}
	if owner, ok := registry.alias[provider.ID]; ok {
		panic("provider id " + provider.ID + " conflicts with alias for " + owner)
	}
	for _, alias := range provider.Aliases {
		if _, ok := registry.byID[alias]; ok {
			panic("provider alias " + alias + " conflicts with registered provider id")
		}
		if owner, ok := registry.alias[alias]; ok {
			panic("provider alias " + alias + " already registered for " + owner)
		}
	}
	registry.byID[provider.ID] = provider
	for _, alias := range provider.Aliases {
		registry.alias[alias] = provider.ID
	}
}

func ActionHandler(provider Provider, id string) (actions.Handler, bool) {
	if provider.Handlers == nil {
		return nil, false
	}
	handler, ok := provider.Handlers[id]
	return handler, ok
}

func Lookup(id string) (Provider, bool) {
	id = strings.TrimSpace(id)
	registry.RLock()
	defer registry.RUnlock()
	provider, ok := registry.byID[id]
	if ok {
		return cloneProvider(provider), true
	}
	providerID, ok := registry.alias[id]
	if !ok {
		return Provider{}, false
	}
	provider, ok = registry.byID[providerID]
	return cloneProvider(provider), ok
}

func All() []Provider {
	registry.RLock()
	defer registry.RUnlock()
	providers := make([]Provider, 0, len(registry.byID))
	for _, provider := range registry.byID {
		providers = append(providers, cloneProvider(provider))
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].ID < providers[j].ID
	})
	return providers
}

func CommandSpecs() []actions.Spec {
	var specs []actions.Spec
	for _, provider := range All() {
		specs = append(specs, ActionSpecs(provider)...)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func ActionSpecs(provider Provider) []actions.Spec {
	if len(provider.Tree.Children) == 0 {
		return nil
	}
	return actions.LeafSpecs(actions.ProviderName(provider.ID), provider.Tree)
}

func cloneProvider(provider Provider) Provider {
	provider.Aliases = slices.Clone(provider.Aliases)
	provider.Handlers = maps.Clone(provider.Handlers)
	return provider
}
