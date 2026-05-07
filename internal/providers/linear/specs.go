package linear

import "github.com/fiam/supacli/internal/policy"

func CommandSpecs() []policy.CommandSpec {
	return []policy.CommandSpec{
		spec("linear.issues.list", []string{"linear", "issues", "list"}, "issue", "list", "read", nil, []string{ScopeRead}),
		spec("linear.issue.get", []string{"linear", "issue", "get"}, "issue", "read", "read", nil, []string{ScopeRead}),
		spec("linear.issue.create", []string{"linear", "issue", "create"}, "issue", "create", "write", []string{"ticket-write"}, []string{ScopeIssuesCreate}),
		spec("linear.comment.add", []string{"linear", "comment", "add"}, "comment", "create", "write", []string{"comment-write"}, []string{ScopeCommentsCreate}),
	}
}

func spec(id string, path []string, resource, action, effect string, risk, scopes []string) policy.CommandSpec {
	return policy.CommandSpec{
		ID:       id,
		Path:     path,
		Provider: "linear",
		Resource: resource,
		Action:   action,
		Effect:   effect,
		Risk:     risk,
		Scopes:   scopes,
	}
}
