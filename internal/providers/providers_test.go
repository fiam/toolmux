package providers_test

import (
	"testing"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers"
	_ "github.com/fiam/toolmux/internal/providers/all"
)

func TestCommandSpecsHaveRequiredFields(t *testing.T) {
	t.Parallel()
	specs := providers.CommandSpecs()
	if len(specs) == 0 {
		t.Fatal("expected command specs")
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.ID == "" || spec.Provider == "" || spec.Resource == "" || spec.Action == "" {
			t.Fatalf("incomplete command spec: %#v", spec)
		}
		if spec.RemoteEffect == "" || spec.LocalEffect == "" {
			t.Fatalf("missing action effects for %s: %#v", spec.ID, spec)
		}
		if len(spec.Path) == 0 {
			t.Fatalf("missing command path for %s", spec.ID)
		}
		if spec.Use == "" || spec.Short == "" {
			t.Fatalf("missing command display metadata for %s: %#v", spec.ID, spec)
		}
		for _, flag := range spec.Flags {
			if flag.Name == "" || flag.Type == "" || flag.Usage == "" {
				t.Fatalf("incomplete flag metadata for %s: %#v", spec.ID, flag)
			}
		}
		switch spec.Provider {
		case "jira", "slack", "google", "google-docs", "google-drive", "gmail":
			t.Fatalf("unimplemented provider command should not be registered: %#v", spec)
		}
		if seen[spec.ID] {
			t.Fatalf("duplicate command spec %s", spec.ID)
		}
		seen[spec.ID] = true
	}
}

func TestCommandTreesHaveRequiredFields(t *testing.T) {
	t.Parallel()
	seenTree := false
	for _, provider := range providers.All() {
		if len(provider.Tree.Children) == 0 {
			continue
		}
		seenTree = true
		actions.Walk(actions.ProviderName(provider.ID), provider.Tree, func(spec actions.Spec) {
			if len(spec.Children) > 0 && (len(spec.Path) == 0 || spec.Use == "" || spec.Short == "") {
				t.Fatalf("incomplete command group for %s: %#v", provider.ID, spec)
			}
		})
	}
	if !seenTree {
		t.Fatal("expected command trees")
	}
}

func TestProviderActionSpecsFlattenTrees(t *testing.T) {
	t.Parallel()
	for _, provider := range providers.All() {
		if len(provider.Tree.Children) == 0 {
			continue
		}
		specs := providers.ActionSpecs(provider)
		if len(specs) == 0 {
			t.Fatalf("expected action specs for %s", provider.ID)
		}
	}
}

func TestLookupGoogleAliases(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"google", "google-docs", "google-drive", "gmail"} {
		provider, ok := providers.Lookup(id)
		if !ok {
			t.Fatalf("expected %s lookup to succeed", id)
		}
		if provider.ID != "google" {
			t.Fatalf("expected google provider for %s, got %s", id, provider.ID)
		}
	}
}
