package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrDenied = errors.New("policy denied command")

type Engine struct {
	policies []loadedPolicy
}

type loadedPolicy struct {
	path   string
	policy Policy
}

func NewEngine(policies ...Policy) *Engine {
	loaded := make([]loadedPolicy, 0, len(policies))
	for _, p := range policies {
		loaded = append(loaded, loadedPolicy{policy: p})
	}
	return &Engine{policies: loaded}
}

func LoadFile(path string) (Policy, error) {
	// #nosec G304 -- policy paths are explicit user configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Policy{}, err
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Default == "" {
		p.Default = "deny"
	}
	if p.Roles == nil {
		p.Roles = map[string]Role{}
	}
	return p, nil
}

func Discover(explicitPath, startDir string) ([]string, error) {
	if explicitPath != "" {
		return []string{explicitPath}, nil
	}
	if env := os.Getenv("TOOLMUX_POLICY"); env != "" {
		return []string{env}, nil
	}
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	var paths []string
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}
	for {
		candidate := filepath.Join(dir, ".toolmux", "policy.yaml")
		if _, err := os.Stat(candidate); err == nil {
			paths = append([]string{candidate}, paths...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return paths, nil
}

func LoadDiscovered(explicitPath, startDir string) (*Engine, []string, error) {
	paths, err := Discover(explicitPath, startDir)
	if err != nil {
		return nil, nil, err
	}
	loaded := make([]loadedPolicy, 0, len(paths))
	for _, path := range paths {
		p, err := LoadFile(path)
		if err != nil {
			return nil, nil, err
		}
		loaded = append(loaded, loadedPolicy{path: path, policy: p})
	}
	return &Engine{policies: loaded}, paths, nil
}

func (e *Engine) Authorize(inv Invocation) Decision {
	if e == nil || len(e.policies) == 0 {
		return Decision{Allowed: true, Reason: "no policy configured"}
	}

	allowed := true
	reason := "allowed by default"
	ruleID := ""

	for _, loaded := range e.policies {
		decision := evaluatePolicy(loaded.policy, inv)
		if loaded.path != "" && decision.Rule != "" {
			decision.Rule = loaded.path + ":" + decision.Rule
		}
		if !decision.Allowed {
			return decision
		}
		if decision.Rule != "" {
			allowed = true
			reason = decision.Reason
			ruleID = decision.Rule
		}
	}

	return Decision{Allowed: allowed, Reason: reason, Rule: ruleID}
}

func (e *Engine) Require(inv Invocation) error {
	decision := e.Authorize(inv)
	if decision.Allowed {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrDenied, decision.Reason)
}

func evaluatePolicy(p Policy, inv Invocation) Decision {
	roles := boundRoles(p, inv)
	defaultAllow := strings.EqualFold(p.Default, "allow")

	var allowMatch *string
	for _, roleName := range roles {
		roleRules := collectRules(p, roleName, map[string]bool{})
		for _, ref := range roleRules.deny {
			if ruleMatches(ref.rule, inv) {
				rule := ref.name
				return Decision{
					Allowed: false,
					Reason:  "denied by policy rule " + rule,
					Rule:    rule,
				}
			}
		}
		for _, ref := range roleRules.allow {
			if ruleMatches(ref.rule, inv) {
				rule := ref.name
				allowMatch = &rule
			}
		}
	}

	if defaultAllow {
		return Decision{Allowed: true, Reason: "allowed by policy default"}
	}
	if allowMatch != nil {
		return Decision{
			Allowed: true,
			Reason:  "allowed by policy rule " + *allowMatch,
			Rule:    *allowMatch,
		}
	}
	return Decision{
		Allowed: false,
		Reason:  "no allow rule matched command " + inv.Spec.ID,
	}
}

type ruleSet struct {
	allow []ruleRef
	deny  []ruleRef
}

type ruleRef struct {
	name string
	rule Rule
}

func collectRules(p Policy, roleName string, seen map[string]bool) ruleSet {
	if seen[roleName] {
		return ruleSet{}
	}
	seen[roleName] = true

	role, ok := p.Roles[roleName]
	if !ok {
		return ruleSet{}
	}
	var out ruleSet
	for _, parent := range role.Extends {
		parentRules := collectRules(p, parent, seen)
		out.allow = append(out.allow, parentRules.allow...)
		out.deny = append(out.deny, parentRules.deny...)
	}
	for i, rule := range role.Allow {
		out.allow = append(out.allow, ruleRef{name: ruleName(roleName, "allow", i, rule), rule: rule})
	}
	for i, rule := range role.Deny {
		out.deny = append(out.deny, ruleRef{name: ruleName(roleName, "deny", i, rule), rule: rule})
	}
	return out
}

func ruleName(roleName, kind string, index int, rule Rule) string {
	if rule.ID != "" {
		return rule.ID
	}
	return fmt.Sprintf("roles.%s.%s[%d]", roleName, kind, index)
}

func boundRoles(p Policy, inv Invocation) []string {
	if len(p.Bindings) == 0 {
		roles := make([]string, 0, len(p.Roles))
		for role := range p.Roles {
			roles = append(roles, role)
		}
		return roles
	}
	var roles []string
	for _, binding := range p.Bindings {
		if stringListMatches(binding.Profiles, inv.Profile) &&
			stringListMatches(binding.Accounts, inv.Account) {
			roles = append(roles, binding.Role)
		}
	}
	return roles
}

func ruleMatches(rule Rule, inv Invocation) bool {
	if rule.Command != "" && !globMatch(rule.Command, inv.Spec.ID) {
		return false
	}
	if rule.Provider != "" && !globMatch(rule.Provider, inv.Spec.Provider) {
		return false
	}
	if len(rule.Resources) > 0 && !stringListMatches(rule.Resources, inv.Spec.Resource) {
		return false
	}
	if len(rule.Actions) > 0 && !stringListMatches(rule.Actions, inv.Spec.Action) {
		return false
	}
	if len(rule.Effects) > 0 && !stringListMatches(rule.Effects, inv.Spec.Effect) {
		return false
	}
	if len(rule.Risks) > 0 && !anyStringMatches(rule.Risks, inv.Spec.Risk) {
		return false
	}
	if len(rule.Profiles) > 0 && !stringListMatches(rule.Profiles, inv.Profile) {
		return false
	}
	if len(rule.Accounts) > 0 && !stringListMatches(rule.Accounts, inv.Account) {
		return false
	}
	return true
}

func stringListMatches(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if globMatch(pattern, value) {
			return true
		}
	}
	return false
}

func anyStringMatches(patterns, values []string) bool {
	for _, value := range values {
		if stringListMatches(patterns, value) {
			return true
		}
	}
	return false
}

func globMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	ok, err := filepath.Match(pattern, value)
	if err != nil {
		return pattern == value
	}
	return ok
}
