package supaclidtest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiam/supacli/internal/actions"
	"github.com/fiam/supacli/internal/providers/brokers"
	_ "github.com/fiam/supacli/internal/providers/brokers/all"
	"github.com/fiam/supacli/internal/providers/notion"
	"github.com/fiam/supacli/internal/server"
)

type Server struct {
	*httptest.Server
}

func New(t testing.TB, config server.Config) *Server {
	t.Helper()
	srv := httptest.NewServer(server.NewHandlerWithConfig(config))
	t.Cleanup(srv.Close)
	return &Server{Server: srv}
}

func NewNotion(t testing.TB, upstreamURL string, upstreamClient *http.Client) *Server {
	t.Helper()
	return New(t, NotionConfig(upstreamURL, upstreamClient))
}

func NotionConfig(upstreamURL string, upstreamClient *http.Client) server.Config {
	upstreamURL = strings.TrimRight(upstreamURL, "/")
	return server.Config{
		Providers: map[actions.ProviderName]brokers.Config{
			notion.ProviderName: {
				ClientID:   "client-id",
				Secret:     "client-secret",
				AuthURL:    upstreamURL + "/oauth/authorize",
				TokenURL:   upstreamURL + "/oauth/token",
				RevokeURL:  upstreamURL + "/oauth/revoke",
				APIVersion: notion.DefaultVersion,
			},
		},
		HTTPClient: upstreamClient,
		SessionTTL: time.Minute,
	}
}

func (s *Server) Env(key string) string {
	if key == "SUPACLI_SUPACLID_URL" {
		return s.URL
	}
	return ""
}
