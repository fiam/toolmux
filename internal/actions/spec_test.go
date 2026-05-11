package actions

import "testing"

func TestLeafSpecsResolveProviderTree(t *testing.T) {
	t.Parallel()
	tree := Group("linear",
		Short("Operate Linear"),
		Children(
			Group("message",
				Short("Operate messages"),
				Children(
					Command("message.send", "send", RBAC("message", VerbSend, EffectWrite), Short("Send a message")),
				),
			),
		),
	)
	specs := LeafSpecs("linear", tree)
	if len(specs) != 1 {
		t.Fatalf("expected one spec, got %d", len(specs))
	}
	spec := specs[0]
	if spec.ID != "linear.message.send" {
		t.Fatalf("unexpected action id %q", spec.ID)
	}
	if spec.RemoteEffect != string(EffectWrite) || spec.LocalEffect != string(EffectNone) {
		t.Fatalf("unexpected effects: %#v", spec)
	}
	if spec.Effect != string(EffectWrite) {
		t.Fatalf("unexpected broad effect: %#v", spec)
	}
	expectedPath := []string{"linear", "message", "send"}
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
