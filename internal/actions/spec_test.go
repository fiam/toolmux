package actions

import "testing"

func TestLeafSpecsResolveProviderTree(t *testing.T) {
	t.Parallel()
	tree := Group("notion",
		Short("Operate Notion"),
		Children(
			Group("page",
				Short("Operate pages"),
				Children(
					Command("page.read", "read", RBAC("page", VerbRead, EffectRead), Short("Read a page")),
				),
			),
		),
	)
	specs := LeafSpecs("notion", tree)
	if len(specs) != 1 {
		t.Fatalf("expected one spec, got %d", len(specs))
	}
	spec := specs[0]
	if spec.ID != "notion.page.read" {
		t.Fatalf("unexpected action id %q", spec.ID)
	}
	if spec.RemoteEffect != string(EffectRead) || spec.LocalEffect != string(EffectNone) {
		t.Fatalf("unexpected effects: %#v", spec)
	}
	if spec.Effect != string(EffectRead) {
		t.Fatalf("unexpected broad effect: %#v", spec)
	}
	expectedPath := []string{"notion", "page", "read"}
	if !equalStrings(spec.Path, expectedPath) {
		t.Fatalf("expected path %#v, got %#v", expectedPath, spec.Path)
	}
}

func TestCombinedEffectTreatsLocalWriteAsWrite(t *testing.T) {
	t.Parallel()
	effect := CombinedEffect(EffectNone, EffectWrite)
	if effect != EffectWrite {
		t.Fatalf("expected local write to produce broad write effect, got %q", effect)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
