package policy

import "testing"

func TestAuthorizeDefaultDenyAllowsReader(t *testing.T) {
	t.Parallel()
	engine := NewEngine(Policy{
		Version: 1,
		Default: "deny",
		Roles: map[string]Role{
			"reader": {
				Allow: []Rule{{Provider: "*", Actions: []string{"read", "list"}}},
			},
		},
	})

	decision := engine.Authorize(Invocation{
		Spec: CommandSpec{
			ID: "notion.page.get", Provider: "notion", Resource: "page", Action: "read",
		},
		Profile: "default",
	})
	if !decision.Allowed {
		t.Fatalf("expected allow, got %#v", decision)
	}
}

func TestAuthorizeDenyOverridesAllow(t *testing.T) {
	t.Parallel()
	engine := NewEngine(Policy{
		Version: 1,
		Default: "deny",
		Roles: map[string]Role{
			"operator": {
				Allow: []Rule{{Provider: "gmail", Actions: []string{"send"}}},
				Deny:  []Rule{{Provider: "gmail", Actions: []string{"send"}}},
			},
		},
	})

	decision := engine.Authorize(Invocation{
		Spec: CommandSpec{
			ID: "gmail.send", Provider: "gmail", Resource: "message", Action: "send",
		},
		Profile: "default",
	})
	if decision.Allowed {
		t.Fatalf("expected deny, got %#v", decision)
	}
}

func TestBindingLimitsRole(t *testing.T) {
	t.Parallel()
	engine := NewEngine(Policy{
		Version: 1,
		Default: "deny",
		Roles: map[string]Role{
			"operator": {
				Allow: []Rule{{Provider: "jira", Actions: []string{"create"}}},
			},
		},
		Bindings: []Binding{{
			Role: "operator", Profiles: []string{"default"}, Accounts: []string{"*@company.com"},
		}},
	})

	spec := CommandSpec{ID: "jira.issue.create", Provider: "jira", Resource: "issue", Action: "create"}
	allowed := engine.Authorize(Invocation{Spec: spec, Profile: "default", Account: "me@company.com"})
	if !allowed.Allowed {
		t.Fatalf("expected bound account to be allowed: %#v", allowed)
	}
	denied := engine.Authorize(Invocation{Spec: spec, Profile: "default", Account: "me@example.com"})
	if denied.Allowed {
		t.Fatalf("expected unbound account to be denied: %#v", denied)
	}
}

func TestAuthorizeMatchesRemoteAndLocalEffects(t *testing.T) {
	t.Parallel()
	engine := NewEngine(Policy{
		Version: 1,
		Default: "deny",
		Roles: map[string]Role{
			"reader": {
				Allow: []Rule{{
					Provider:      "*",
					RemoteEffects: []string{"read", "none"},
					LocalEffects:  []string{"none"},
				}},
			},
		},
	})

	readSpec := CommandSpec{
		ID: "notion.page.read", Provider: "notion", Resource: "page", Action: "read",
		RemoteEffect: "read", LocalEffect: "none",
	}
	if decision := engine.Authorize(Invocation{Spec: readSpec, Profile: "default"}); !decision.Allowed {
		t.Fatalf("expected read command to be allowed: %#v", decision)
	}

	connectSpec := CommandSpec{
		ID: "notion.connect", Provider: "notion", Resource: "connection", Action: "connect",
		RemoteEffect: "none", LocalEffect: "write",
	}
	if decision := engine.Authorize(Invocation{Spec: connectSpec, Profile: "default"}); decision.Allowed {
		t.Fatalf("expected local write command to be denied: %#v", decision)
	}
}

func TestAllowsReadOnly(t *testing.T) {
	t.Parallel()
	if !AllowsReadOnly(CommandSpec{RemoteEffect: "read", LocalEffect: "none"}) {
		t.Fatal("expected remote read/local none to be read-only")
	}
	if AllowsReadOnly(CommandSpec{RemoteEffect: "read", LocalEffect: "write"}) {
		t.Fatal("expected local write to be blocked in read-only mode")
	}
	if AllowsReadOnly(CommandSpec{RemoteEffect: "write", LocalEffect: "none"}) {
		t.Fatal("expected remote write to be blocked in read-only mode")
	}
}

func TestLegacyEffectsMatchBroadEffect(t *testing.T) {
	t.Parallel()
	engine := NewEngine(Policy{
		Version: 1,
		Default: "deny",
		Roles: map[string]Role{
			"operator": {
				Allow: []Rule{{Effects: []string{"write"}}},
			},
		},
	})

	spec := CommandSpec{
		ID: "notion.connect", Provider: "notion", Resource: "connection", Action: "connect",
		Effect: "write", RemoteEffect: "none", LocalEffect: "write",
	}
	if decision := engine.Authorize(Invocation{Spec: spec, Profile: "default"}); !decision.Allowed {
		t.Fatalf("expected broad write effect to be allowed: %#v", decision)
	}
}
