package providers

import "testing"

func TestCommandSpecsHaveRequiredFields(t *testing.T) {
	specs := CommandSpecs()
	if len(specs) == 0 {
		t.Fatal("expected command specs")
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.ID == "" || spec.Provider == "" || spec.Resource == "" || spec.Action == "" {
			t.Fatalf("incomplete command spec: %#v", spec)
		}
		if len(spec.Path) == 0 {
			t.Fatalf("missing command path for %s", spec.ID)
		}
		if seen[spec.ID] {
			t.Fatalf("duplicate command spec %s", spec.ID)
		}
		seen[spec.ID] = true
	}
}

func TestLookupGoogleAliases(t *testing.T) {
	for _, id := range []string{"google", "google-docs", "google-drive", "gmail"} {
		provider, ok := Lookup(id)
		if !ok {
			t.Fatalf("expected %s lookup to succeed", id)
		}
		if provider.ID != "google" {
			t.Fatalf("expected google provider for %s, got %s", id, provider.ID)
		}
	}
}
