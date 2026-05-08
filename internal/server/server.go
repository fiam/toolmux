package server

import (
	"context"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "supaclid",
		Short: "Run the Supacli local server daemon",
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
	registerOAuthHandlers(mux, config)
	return mux
}

func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}
