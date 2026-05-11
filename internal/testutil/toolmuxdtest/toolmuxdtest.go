package toolmuxdtest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/server"
)

type Server struct {
	URL    string
	client *http.Client
}

func New(t testing.TB, config server.Config) *Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	if config.PublicBaseURL == "" {
		config.PublicBaseURL = "http://" + srv.Listener.Addr().String()
	}
	for id, providerConfig := range config.Providers {
		if providerConfig.RedirectURI == "" {
			providerConfig.RedirectURI = config.PublicBaseURL + "/oauth/" + string(id) + "/callback"
			config.Providers[id] = providerConfig
		}
	}
	srv.Config.Handler = server.NewHandlerWithConfig(config)
	srv.Start()
	t.Cleanup(srv.Close)
	return &Server{URL: srv.URL, client: srv.Client()}
}

func ExternalFromEnv(t testing.TB) (*Server, bool) {
	t.Helper()
	url := strings.TrimRight(strings.TrimSpace(os.Getenv("TOOLMUXD_EXTERNAL_URL")), "/")
	if url == "" {
		return nil, false
	}
	return &Server{URL: url, client: http.DefaultClient}, true
}

func (s *Server) Client() *http.Client {
	if s.client != nil {
		return s.client
	}
	return http.DefaultClient
}

func (s *Server) Env(key string) string {
	if key == "TOOLMUX_TOOLMUXD_URL" {
		return s.URL
	}
	return ""
}
