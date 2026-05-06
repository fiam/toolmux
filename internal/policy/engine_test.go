package policy

import "testing"

func TestAuthorizeDefaultDenyAllowsReader(t *testing.T) {
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
