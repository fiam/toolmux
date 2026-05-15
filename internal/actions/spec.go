package actions

import (
	"slices"
	"strings"
)

type ProviderName string
type LocalName string
type ResourceName string
type Verb string
type Effect string

const (
	EffectNone  Effect = "none"
	EffectRead  Effect = "read"
	EffectWrite Effect = "write"
)

const (
	ResourceConnection ResourceName = "connection"
)

const (
	VerbConnect    Verb = "connect"
	VerbCreate     Verb = "create"
	VerbDelete     Verb = "delete"
	VerbDiagnose   Verb = "diagnose"
	VerbDisconnect Verb = "disconnect"
	VerbList       Verb = "list"
	VerbMove       Verb = "move"
	VerbOpen       Verb = "open"
	VerbQuery      Verb = "query"
	VerbRead       Verb = "read"
	VerbRestore    Verb = "restore"
	VerbRun        Verb = "run"
	VerbSearch     Verb = "search"
	VerbSend       Verb = "send"
	VerbShare      Verb = "share"
	VerbStatus     Verb = "status"
	VerbUpdate     Verb = "update"
)

type Spec struct {
	ID           string   `json:"id" yaml:"id"`
	Segment      string   `json:"segment,omitempty" yaml:"segment,omitempty"`
	Path         []string `json:"path" yaml:"path"`
	Use          string   `json:"use" yaml:"use"`
	Short        string   `json:"short" yaml:"short"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	Aliases      []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Args         ArgsSpec `json:"args,omitzero" yaml:"args,omitempty"`
	Flags        []Flag   `json:"flags,omitempty" yaml:"flags,omitempty"`
	Provider     string   `json:"provider" yaml:"provider"`
	Resource     string   `json:"resource" yaml:"resource"`
	Action       string   `json:"action" yaml:"action"`
	Effect       string   `json:"effect" yaml:"effect"`
	RemoteEffect string   `json:"remote_effect" yaml:"remote_effect"`
	LocalEffect  string   `json:"local_effect" yaml:"local_effect"`
	Risk         []string `json:"risk,omitempty" yaml:"risk,omitempty"`
	Scopes       []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Children     []Spec   `json:"children,omitempty" yaml:"children,omitempty"`
}

type ArgsSpec struct {
	Min int `json:"min,omitempty" yaml:"min,omitempty"`
	Max int `json:"max,omitempty" yaml:"max,omitempty"`
}

type FlagType string

const (
	FlagBool        FlagType = "bool"
	FlagInt         FlagType = "int"
	FlagString      FlagType = "string"
	FlagStringSlice FlagType = "string_slice"
)

type Flag struct {
	Name          string   `json:"name" yaml:"name"`
	Type          FlagType `json:"type" yaml:"type"`
	Usage         string   `json:"usage" yaml:"usage"`
	Default       string   `json:"default,omitempty" yaml:"default,omitempty"`
	DefaultBool   bool     `json:"default_bool,omitempty" yaml:"default_bool,omitempty"`
	DefaultInt    int      `json:"default_int,omitempty" yaml:"default_int,omitempty"`
	DefaultString []string `json:"default_string,omitempty" yaml:"default_string,omitempty"`
}

type Option func(*Spec)

func Command(name LocalName, segment string, opts ...Option) Spec {
	spec := Spec{
		ID:           string(name),
		Segment:      segment,
		RemoteEffect: string(EffectNone),
		LocalEffect:  string(EffectNone),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func Group(segment string, opts ...Option) Spec {
	spec := Spec{Segment: segment}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func RBAC(resource ResourceName, verb Verb, remote Effect, local ...Effect) Option {
	return func(spec *Spec) {
		spec.Resource = string(resource)
		spec.Action = string(verb)
		spec.RemoteEffect = string(remote)
		if len(local) > 0 {
			spec.LocalEffect = string(local[0])
		}
	}
}

func Risks(risks ...string) Option {
	return func(spec *Spec) {
		spec.Risk = append([]string(nil), risks...)
	}
}

func Scopes(scopes ...string) Option {
	return func(spec *Spec) {
		spec.Scopes = append([]string(nil), scopes...)
	}
}

func Use(use string) Option {
	return func(spec *Spec) {
		spec.Use = use
	}
}

func Short(short string) Option {
	return func(spec *Spec) {
		spec.Short = short
	}
}

func Description(description string) Option {
	return func(spec *Spec) {
		spec.Description = description
	}
}

func Aliases(aliases ...string) Option {
	return func(spec *Spec) {
		spec.Aliases = append([]string(nil), aliases...)
	}
}

func Args(min, max int) Option {
	return func(spec *Spec) {
		spec.Args = ArgsSpec{Min: min, Max: max}
	}
}

func ExactArgs(n int) Option {
	return Args(n, n)
}

func MinArgs(n int) Option {
	return Args(n, -1)
}

func MaxArgs(n int) Option {
	return Args(0, n)
}

func StringFlag(name, defaultValue, usage string) Option {
	return addFlag(Flag{Name: name, Type: FlagString, Usage: usage, Default: defaultValue})
}

func BoolFlag(name string, defaultValue bool, usage string) Option {
	return addFlag(Flag{Name: name, Type: FlagBool, Usage: usage, DefaultBool: defaultValue})
}

func IntFlag(name string, defaultValue int, usage string) Option {
	return addFlag(Flag{Name: name, Type: FlagInt, Usage: usage, DefaultInt: defaultValue})
}

func StringSliceFlag(name string, defaultValue []string, usage string) Option {
	return addFlag(Flag{Name: name, Type: FlagStringSlice, Usage: usage, DefaultString: append([]string(nil), defaultValue...)})
}

func addFlag(flag Flag) Option {
	return func(spec *Spec) {
		spec.Flags = append(spec.Flags, flag)
	}
}

func Children(children ...Spec) Option {
	return func(spec *Spec) {
		spec.Children = append([]Spec(nil), children...)
	}
}

func LeafSpecs(provider ProviderName, tree Spec) []Spec {
	specs := []Spec{}
	Walk(provider, tree, func(spec Spec) {
		if len(spec.Children) == 0 && spec.ID != "" {
			specs = append(specs, spec)
		}
	})
	return specs
}

func Walk(provider ProviderName, tree Spec, visit func(Spec)) {
	walk(provider, tree, nil, visit)
}

func walk(provider ProviderName, node Spec, parentPath []string, visit func(Spec)) {
	resolved := Resolve(provider, node, parentPath)
	visit(resolved)
	for _, child := range node.Children {
		walk(provider, child, resolved.Path, visit)
	}
}

func Resolve(provider ProviderName, spec Spec, parentPath []string) Spec {
	resolved := spec
	resolved.Provider = firstNonEmpty(resolved.Provider, string(provider))
	if resolved.Segment != "" {
		resolved.Path = append(append([]string(nil), parentPath...), resolved.Segment)
	} else {
		resolved.Path = append([]string(nil), resolved.Path...)
	}
	resolved.Aliases = append([]string(nil), spec.Aliases...)
	resolved.Flags = slices.Clone(spec.Flags)
	resolved.Risk = append([]string(nil), spec.Risk...)
	resolved.Scopes = append([]string(nil), spec.Scopes...)
	if resolved.Use == "" {
		resolved.Use = resolved.Segment
	}
	if len(resolved.Children) == 0 && resolved.ID != "" {
		resolved.ID = canonicalName(provider, LocalName(resolved.ID))
		remote := Effect(firstNonEmpty(resolved.RemoteEffect, string(EffectNone)))
		local := Effect(firstNonEmpty(resolved.LocalEffect, string(EffectNone)))
		resolved.RemoteEffect = string(remote)
		resolved.LocalEffect = string(local)
		resolved.Effect = string(CombinedEffect(remote, local))
	}
	resolved.Children = append([]Spec(nil), spec.Children...)
	return resolved
}

func CombinedEffect(remote, local Effect) Effect {
	if remote == EffectWrite || local == EffectWrite {
		return EffectWrite
	}
	if remote == EffectRead || local == EffectRead {
		return EffectRead
	}
	return EffectNone
}

func canonicalName(provider ProviderName, local LocalName) string {
	return strings.Trim(string(provider)+"."+string(local), ".")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
