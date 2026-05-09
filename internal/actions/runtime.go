package actions

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/fiam/supacli/internal/credentials"
	"github.com/fiam/supacli/internal/output"
)

type Invocation struct {
	Spec  Spec           `json:"spec"`
	Args  []string       `json:"args"`
	Flags map[string]any `json:"flags,omitempty"`
}

func (inv Invocation) String(name string) string {
	value, _ := inv.Flags[name].(string)
	return value
}

func (inv Invocation) Bool(name string) bool {
	value, _ := inv.Flags[name].(bool)
	return value
}

func (inv Invocation) Int(name string) int {
	value, _ := inv.Flags[name].(int)
	return value
}

func (inv Invocation) StringSlice(name string) []string {
	value, _ := inv.Flags[name].([]string)
	return slices.Clone(value)
}

type Context struct {
	Context       context.Context
	Credentials   credentials.Store
	HTTPClient    *http.Client
	Profile       string
	Account       string
	Provider      string
	ProviderURL   string
	ProviderAPI   string
	SupaclidURL   string
	Interactive   bool
	ReadFile      func(string) ([]byte, error)
	OpenBrowser   func(string) error
	SelectString  func(context.Context, SelectStringRequest) (string, bool, error)
	SelectInteger func(context.Context, SelectIntegerRequest) (int, bool, error)
}

type SelectStringRequest struct {
	Title       string
	Description string
	Options     []SelectStringOption
	Height      int
	Filtering   bool
}

type SelectStringOption struct {
	Label string
	Value string
}

type SelectIntegerRequest struct {
	Title       string
	Description string
	Options     []SelectIntegerOption
	Height      int
	Filtering   bool
}

type SelectIntegerOption struct {
	Label string
	Value int
}

type Handler func(Context, Invocation) (any, error)

type ConnectionStatus struct {
	Provider      string            `json:"provider"`
	DisplayName   string            `json:"display_name"`
	Profile       string            `json:"profile"`
	Account       string            `json:"account"`
	Connected     bool              `json:"connected"`
	TokenType     string            `json:"token_type,omitempty"`
	ExpiresAt     time.Time         `json:"expires_at,omitzero"`
	Scopes        []string          `json:"scopes,omitempty"`
	Permissions   []string          `json:"permissions,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
	WorkspaceID   string            `json:"workspace_id,omitempty"`
	WorkspaceName string            `json:"workspace_name,omitempty"`
	Message       string            `json:"message,omitempty"`
}

type Diagnostic struct {
	Provider    string            `json:"provider,omitempty"`
	Check       string            `json:"check"`
	Status      string            `json:"status"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

type DiagnosticsFunc func(context.Context, Context, ConnectionStatus) []Diagnostic

type TableRenderable interface {
	Table(output.Options) output.Table
}

type TextRenderable interface {
	Text() string
}

type MarkdownRenderable interface {
	MarkdownSource() string
	MarkdownTruncated() (bool, int)
}

type BrowserOpenRenderable interface {
	BrowserURL() string
	BrowserURLOnly() bool
}

type FollowRenderable interface {
	Follow(Context) (any, bool, error)
}

type DryRun struct {
	DryRun  bool   `json:"dry_run"`
	Action  string `json:"action"`
	Request any    `json:"request"`
}

func NewDryRun(action string, request any) DryRun {
	return DryRun{DryRun: true, Action: action, Request: request}
}

func (dryRun DryRun) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Dry run", "true"},
			{"Action", dryRun.Action},
			{"Request", "use --output json to inspect payload"},
		},
	}
}
