package linear

import "testing"

func TestCommandSpecsUseLinearScopes(t *testing.T) {
	specs := CommandSpecs()
	if len(specs) != 4 {
		t.Fatalf("expected 4 Linear command specs, got %d", len(specs))
	}
	required := map[string]string{
		"linear.issues.list":  ScopeRead,
		"linear.issue.get":    ScopeRead,
		"linear.issue.create": ScopeIssuesCreate,
		"linear.comment.add":  ScopeCommentsCreate,
	}
	for _, spec := range specs {
		want := required[spec.ID]
		if want == "" {
			t.Fatalf("unexpected spec %s", spec.ID)
		}
		if len(spec.Scopes) != 1 || spec.Scopes[0] != want {
			t.Fatalf("scope mismatch for %s: %#v", spec.ID, spec.Scopes)
		}
	}
}
