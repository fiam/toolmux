package server

import (
	"context"
	"encoding/json"
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
