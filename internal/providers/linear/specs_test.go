package linear

import "testing"

func TestCommandSpecsUseLinearScopes(t *testing.T) {
	specs := CommandSpecs()
	if len(specs) != 6 {
		t.Fatalf("expected 6 Linear command specs, got %d", len(specs))
	}
	required := map[string]string{
		"linear.issues.list":  ScopeRead,
		"linear.issue.get":    ScopeRead,
		"linear.issue.create": ScopeIssuesCreate,
		"linear.comment.add":  ScopeCommentsCreate,
	}
	for _, spec := range specs {
		if spec.ID == "linear.status" || spec.ID == "linear.doctor" {
			if len(spec.Scopes) != 0 {
				t.Fatalf("%s should not require scopes: %#v", spec.ID, spec.Scopes)
			}
			continue
		}
		want := required[spec.ID]
		if want == "" {
			t.Fatalf("unexpected spec %s", spec.ID)
		}
		if len(spec.Scopes) != 1 || spec.Scopes[0] != want {
			t.Fatalf("scope mismatch for %s: %#v", spec.ID, spec.Scopes)
		}
	}
}
