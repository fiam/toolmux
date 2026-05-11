package toolmuxdtest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/brokers"
	_ "github.com/fiam/toolmux/internal/providers/brokers/all"
	"github.com/fiam/toolmux/internal/providers/slack"
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

func NewSlack(t testing.TB, upstreamURL string, upstreamClient *http.Client) *Server {
	t.Helper()
	return New(t, SlackConfig(upstreamURL, upstreamClient))
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

func SlackConfig(upstreamURL string, upstreamClient *http.Client) server.Config {
	upstreamURL = strings.TrimRight(upstreamURL, "/")
	return server.Config{
		Providers: map[actions.ProviderName]brokers.Config{
			slack.ProviderName: {
				ClientID:  "slack-client-id",
				Secret:    "slack-client-secret",
				AuthURL:   upstreamURL + "/oauth/v2/authorize",
				TokenURL:  upstreamURL + "/api/oauth.v2.access",
				RevokeURL: upstreamURL + "/api/auth.revoke",
			},
		},
		HTTPClient: upstreamClient,
		SessionTTL: time.Minute,
	}
}

func (s *Server) Env(key string) string {
	if key == "TOOLMUX_TOOLMUXD_URL" {
		return s.URL
	}
	return ""
}
