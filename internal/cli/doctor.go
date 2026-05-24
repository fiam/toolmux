package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
)

type providerDiagnostic = actions.Diagnostic

func doctorCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Toolmux setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			diagnostics := coreDiagnostics(opts)
			if diagnosticsHaveFailure(diagnostics) {
				return writeDiagnostics(cmd, opts, diagnostics)
			}
			store, err := opts.credentials()
			if err != nil {
				diagnostics = append(diagnostics, providerDiagnostic{
					Check:       "credential-store",
					Status:      "fail",
					Message:     err.Error(),
					Remediation: "Check OS credential store availability or run provider commands with a supported keyring backend.",
				})
				return writeDiagnostics(cmd, opts, diagnostics)
			}
			diagnostics = append(diagnostics, credentialStoreDiagnostic(cmd.Context(), store))
			diagnostics = append(diagnostics, mcpRemoteDoctorDiagnostics(cmd.Context(), opts, store)...)
			return writeDiagnostics(cmd, opts, diagnostics)
		},
	}
}

func diagnosticsHaveFailure(diagnostics []providerDiagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Status == "fail" {
			return true
		}
	}
	return false
}

func coreDiagnostics(opts *options) []providerDiagnostic {
	_, paths, err := policy.LoadDiscovered(opts.policy, "")
	if err != nil {
		return []providerDiagnostic{{
			Check:       "policy",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Fix the policy file or pass --policy with a readable policy path.",
		}}
	}
	if len(paths) == 0 {
		return []providerDiagnostic{{
			Check:   "policy",
			Status:  "warn",
			Message: "no policy configured",
		}}
	}
	return []providerDiagnostic{{
		Check:   "policy",
		Status:  "ok",
		Message: "loaded " + strings.Join(paths, ", "),
	}}
}

func credentialStoreDiagnostic(ctx context.Context, store credentials.Store) providerDiagnostic {
	diagnostics := store.Doctor(ctx)
	status := "fail"
	if diagnostics.Available {
		status = "ok"
	}
	return providerDiagnostic{
		Check:   "credential-store",
		Status:  status,
		Message: diagnostics.Message,
		Details: map[string]string{
			"backend": diagnostics.Backend,
			"service": diagnostics.Service,
		},
	}
}

func mcpRemoteDoctorDiagnostics(ctx context.Context, opts *options, store credentials.Store) []providerDiagnostic {
	entries, err := effectiveMCPRemoteServerEntries(opts.workDir)
	if err != nil {
		return []providerDiagnostic{{
			Check:       "mcp-config",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Fix Toolmux MCP config or remove invalid MCP server entries.",
		}}
	}
	if len(entries) == 0 {
		return []providerDiagnostic{{
			Check:       "toolboxes",
			Status:      "warn",
			Message:     "no toolboxes registered",
			Remediation: "Run `toolmux add <catalog-name-or-url>`.",
		}}
	}
	diagnostics := []providerDiagnostic{{
		Check:   "toolboxes",
		Status:  "ok",
		Message: fmt.Sprintf("%d registered", len(entries)),
	}}
	for _, entry := range entries {
		diagnostics = append(diagnostics, mcpRemoteCacheDiagnostic(opts, entry))
		diagnostics = append(diagnostics, mcpRemoteAuthDiagnostic(ctx, opts, store, entry))
	}
	return diagnostics
}

func mcpRemoteCacheDiagnostic(opts *options, entry mcpRemoteServerEntry) providerDiagnostic {
	diagnostic := providerDiagnostic{
		Provider: entry.Name,
		Check:    "toolbox-cache",
		Status:   "warn",
	}
	cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	if err != nil {
		diagnostic.Status = "fail"
		diagnostic.Message = err.Error()
		diagnostic.Remediation = "Remove the corrupt cache or run `toolmux mcp sync " + entry.Name + "`."
		return diagnostic
	}
	if !ok {
		diagnostic.Message = "no cached tools"
		diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
		return diagnostic
	}
	diagnostic.Status = "ok"
	diagnostic.Message = fmt.Sprintf("%d cached tools", len(cache.Tools))
	if time.Since(cache.SyncedAt) > mcpRemoteCacheMaxAge {
		diagnostic.Status = "warn"
		diagnostic.Message = "cached tools are stale"
		diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
	}
	return diagnostic
}

func mcpRemoteAuthDiagnostic(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry) providerDiagnostic {
	diagnostic := providerDiagnostic{
		Provider: entry.Name,
		Check:    "toolbox-auth",
		Status:   "ok",
		Message:  "auth not required",
	}
	if entry.Server.Transport == mcpRemoteTransportStdio {
		return diagnostic
	}
	tokens, err := store.LoadOAuthTokens(ctx, mcpRemoteCredentialRef(opts, entry.Name))
	if err != nil {
		if !errors.Is(err, credentials.ErrNotFound) {
			diagnostic.Status = "fail"
			diagnostic.Message = err.Error()
			diagnostic.Remediation = "Check OS credential store availability."
			return diagnostic
		}
		if entry.Server.AuthRequired != nil && *entry.Server.AuthRequired {
			diagnostic.Status = "warn"
			diagnostic.Message = "auth required but not stored"
			diagnostic.Remediation = "Run `toolmux mcp auth login " + entry.Name + "` or `toolmux mcp auth set " + entry.Name + "`."
			return diagnostic
		}
		if entry.Server.AuthRequired == nil {
			diagnostic.Status = "warn"
			diagnostic.Message = "auth requirement unknown"
			diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
		}
		return diagnostic
	}
	if mcpRemoteStoredTokenIsOAuth(tokens) {
		diagnostic.Message = "OAuth auth stored"
		return diagnostic
	}
	diagnostic.Message = "bearer auth stored"
	return diagnostic
}

func writeDiagnostics(cmd *cobra.Command, opts *options, diagnostics []providerDiagnostic) error {
	return writeValue(cmd, opts, diagnostics, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			target := firstNonEmpty(diagnostic.Provider, "toolmux")
			rows = append(rows, []string{
				target,
				diagnostic.Check,
				output.StatusBadge(human, diagnostic.Status),
				output.Value(diagnostic.Message),
				output.Value(diagnostic.Remediation),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Target", "Check", "Status", "Message", "Remediation"},
			Rows:    rows,
			Empty:   "no diagnostics",
		})
	})
}
