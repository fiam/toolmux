package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/version"
)

func NewCommand() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "toolmuxd",
		Short: "Run the Toolmux local server daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := &http.Server{
				Addr:              addr,
				Handler:           NewHandler(),
				ReadHeaderTimeout: 5 * time.Second,
			}
			return server.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	return cmd
}

func NewHandler() http.Handler {
	return NewHandlerWithConfig(ConfigFromEnv())
}

func NewHandlerWithConfig(config Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /build", buildInfo)
	registerOAuthHandlers(mux, config)
	return mux
}

func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}

func buildInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")
	info := version.CurrentBuildInfo("toolmuxd")
	if wantsText(r) {
		writeText(w, http.StatusOK, renderBuildInfo(info))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func wantsText(r *http.Request) bool {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "text" || format == "plain" {
		return true
	}
	if format == "json" {
		return false
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/plain") && !strings.Contains(accept, "application/json")
}

func renderBuildInfo(info version.BuildInfo) string {
	var out strings.Builder
	fmt.Fprintf(&out, "service: %s\n", info.Service)
	fmt.Fprintf(&out, "version: %s\n", info.Version)
	if info.GoVersion != "" {
		fmt.Fprintf(&out, "go_version: %s\n", info.GoVersion)
	}
	if info.Module != "" {
		fmt.Fprintf(&out, "module: %s\n", info.Module)
	}
	if info.VCS != nil {
		if info.VCS.Revision != "" {
			fmt.Fprintf(&out, "vcs_revision: %s\n", info.VCS.Revision)
		}
		if info.VCS.Time != "" {
			fmt.Fprintf(&out, "vcs_time: %s\n", info.VCS.Time)
		}
		fmt.Fprintf(&out, "vcs_modified: %t\n", info.VCS.Modified)
	}
	return out.String()
}

func writeText(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(value))
}
