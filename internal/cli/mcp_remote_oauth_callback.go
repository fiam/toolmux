package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type mcpRemoteOAuthCallback struct {
	redirectURI string
	server      *http.Server
	results     <-chan mcpRemoteOAuthCallbackResult
}

func startMCPRemoteOAuthCallback(port int, state string, page mcpRemoteOAuthCallbackPage) (mcpRemoteOAuthCallback, error) {
	if port < 0 || port > 65535 {
		return mcpRemoteOAuthCallback{}, fmt.Errorf("redirect port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return mcpRemoteOAuthCallback{}, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return mcpRemoteOAuthCallback{}, fmt.Errorf("OAuth callback listener did not return a TCP address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, mcpRemoteOAuthCallbackPath)
	results := make(chan mcpRemoteOAuthCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(mcpRemoteOAuthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		result := mcpRemoteOAuthCallbackResult{}
		switch {
		case query.Get("state") != state:
			result.Err = fmt.Errorf("MCP OAuth callback state mismatch")
		case query.Get("error") != "":
			result.Err = fmt.Errorf("MCP OAuth callback error: %s", query.Get("error"))
		case strings.TrimSpace(query.Get("code")) == "":
			result.Err = fmt.Errorf("MCP OAuth callback did not include a code")
		default:
			result.Code = query.Get("code")
		}
		writeMCPRemoteOAuthCallbackPage(w, page, result)
		select {
		case results <- result:
		default:
		}
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- mcpRemoteOAuthCallbackResult{Err: err}:
			default:
			}
		}
	}()
	return mcpRemoteOAuthCallback{
		redirectURI: redirectURI,
		server:      server,
		results:     results,
	}, nil
}

func (callback mcpRemoteOAuthCallback) wait(ctx context.Context, timeout time.Duration) (mcpRemoteOAuthCallbackResult, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-callback.results:
		return result, nil
	case <-ctx.Done():
		return mcpRemoteOAuthCallbackResult{}, fmt.Errorf("timed out waiting for MCP OAuth callback: %w", ctx.Err())
	}
}

func (callback mcpRemoteOAuthCallback) shutdown() {
	if callback.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = callback.server.Shutdown(ctx)
}

type mcpRemoteOAuthCallbackPage struct {
	ServerName  string
	DisplayName string
	LogoSlug    string
	LogoText    string
}

type mcpRemoteOAuthCallbackPageView struct {
	Page    mcpRemoteOAuthCallbackPage
	Success bool
	Error   string
}

func writeMCPRemoteOAuthCallbackPage(w http.ResponseWriter, page mcpRemoteOAuthCallbackPage, result mcpRemoteOAuthCallbackResult) {
	page = normalizeMCPRemoteOAuthCallbackPage(page)
	view := mcpRemoteOAuthCallbackPageView{
		Page:    page,
		Success: result.Err == nil,
	}
	status := http.StatusOK
	if result.Err != nil {
		status = http.StatusBadRequest
		view.Error = result.Err.Error()
	}
	var body bytes.Buffer
	if err := mcpRemoteOAuthCallbackTemplate.Execute(&body, view); err != nil {
		http.Error(w, "render MCP OAuth callback page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

func normalizeMCPRemoteOAuthCallbackPage(page mcpRemoteOAuthCallbackPage) mcpRemoteOAuthCallbackPage {
	page.ServerName = strings.TrimSpace(page.ServerName)
	if page.ServerName == "" {
		page.ServerName = "server"
	}
	page.DisplayName = strings.TrimSpace(page.DisplayName)
	if page.DisplayName == "" {
		page.DisplayName = humanMCPRemoteName(page.ServerName)
	}
	page.LogoSlug = strings.TrimSpace(page.LogoSlug)
	if page.LogoSlug == "" {
		page.LogoSlug = "generic"
	}
	page.LogoText = strings.TrimSpace(page.LogoText)
	if page.LogoText == "" {
		page.LogoText = mcpRemoteLogoText(page.DisplayName)
	}
	return page
}

func mcpRemoteOAuthCallbackPageFor(entry mcpRemoteServerEntry, discovery mcpRemoteOAuthDiscovery) mcpRemoteOAuthCallbackPage {
	_, catalogDefinition, inCatalog := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server)
	catalogDisplayName := ""
	if inCatalog {
		catalogDisplayName = catalogDefinition.DisplayName
	}
	logoSlug := mcpRemoteKnownLogoSlug(
		entry.Name,
		catalogDisplayName,
		entry.Server.URL,
		discovery.Resource.ResourceName,
		discovery.ResourceURI,
		discovery.Authorization.Issuer,
	)
	displayName := firstNonEmpty(
		strings.TrimSpace(discovery.Resource.ResourceName),
		catalogDisplayName,
		humanMCPRemoteName(entry.Name),
		"MCP server",
	)
	return normalizeMCPRemoteOAuthCallbackPage(mcpRemoteOAuthCallbackPage{
		ServerName:  entry.Name,
		DisplayName: displayName,
		LogoSlug:    logoSlug,
		LogoText:    mcpRemoteLogoText(displayName),
	})
}

func mcpRemoteKnownLogoSlug(values ...string) string {
	target := normalizeMCPRemoteLogoMatch(strings.Join(values, " "))
	catalog := mcpBuiltinRemoteCatalog()
	type candidate struct {
		slug  string
		match string
	}
	candidates := make([]candidate, 0, len(catalog)*2)
	for name, definition := range catalog {
		candidates = append(candidates, candidate{slug: name, match: normalizeMCPRemoteLogoMatch(name)})
		if displayName := normalizeMCPRemoteLogoMatch(definition.DisplayName); displayName != "" {
			candidates = append(candidates, candidate{slug: name, match: displayName})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].match) != len(candidates[j].match) {
			return len(candidates[i].match) > len(candidates[j].match)
		}
		return candidates[i].slug < candidates[j].slug
	})
	for _, candidate := range candidates {
		if candidate.match != "" && strings.Contains(target, candidate.match) {
			return candidate.slug
		}
	}
	return "generic"
}

func normalizeMCPRemoteLogoMatch(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastSpace := true
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func humanMCPRemoteName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "MCP server"
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(parts) == 0 {
		return name
	}
	for i, part := range parts {
		parts[i] = titleMCPRemoteNamePart(part)
	}
	return strings.Join(parts, " ")
}

func titleMCPRemoteNamePart(part string) string {
	runes := []rune(part)
	if len(runes) == 0 {
		return part
	}
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

func mcpRemoteLogoText(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "M"
}

var mcpRemoteOAuthCallbackTemplate = template.Must(template.New("mcp-remote-oauth-callback").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Page.DisplayName}} {{if .Success}}connected{{else}}authorization failed{{end}} - Toolmux</title>
  <style>
    :root {
      color-scheme: dark;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      background: #0a0d12;
      color: #e8edf7;
    }
    * {
      box-sizing: border-box;
    }
    body {
      min-height: 100vh;
      margin: 0;
      display: grid;
      place-items: center;
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.04), transparent 30%),
        #0a0d12;
    }
    main {
      width: min(760px, calc(100vw - 32px));
      border: 1px solid rgba(255, 255, 255, 0.16);
      border-radius: 8px;
      background: rgba(14, 18, 27, 0.96);
      box-shadow: 0 24px 80px rgba(0, 0, 0, 0.38);
      overflow: hidden;
    }
    header {
      display: flex;
      align-items: center;
      gap: 18px;
      padding: 28px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.12);
    }
    .logo {
      width: 56px;
      height: 56px;
      flex: 0 0 auto;
      display: grid;
      place-items: center;
      border-radius: 8px;
      background: #ffffff;
      color: #111111;
      border: 1px solid rgba(255, 255, 255, 0.2);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 30px;
      font-weight: 800;
      line-height: 1;
    }
    .logo svg {
      width: 38px;
      height: 38px;
      display: block;
    }
    .logo-linear {
      background: #111111;
    }
    .logo-miro {
      background: #ffd02f;
    }
    .logo-atlassian {
      background: #0052cc;
    }
    .logo-grafana {
      background: #f46800;
      color: #ffffff;
    }
    .logo-generic {
      background: #1d9a8a;
      color: #ffffff;
    }
    .eyebrow {
      margin: 0 0 8px;
      color: #8ea0b8;
      font-size: 12px;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    h1 {
      margin: 0;
      font-size: clamp(24px, 4vw, 36px);
      line-height: 1.12;
      letter-spacing: 0;
    }
    .terminal {
      margin: 28px;
      padding: 20px;
      border-radius: 8px;
      background: #05070a;
      border: 1px solid rgba(255, 255, 255, 0.12);
      color: #cbd6e6;
      font-size: 15px;
      line-height: 1.8;
      overflow-wrap: anywhere;
    }
    .prompt {
      color: #7dd3fc;
    }
    .ok {
      color: #86efac;
      font-weight: 700;
    }
    .err {
      color: #fca5a5;
      font-weight: 700;
    }
    .muted {
      color: #8ea0b8;
    }
    .error-detail {
      color: #fca5a5;
    }
    .hint {
      margin: 0;
      padding: 0 28px 28px;
      color: #a8b4c6;
      line-height: 1.55;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    @media (max-width: 520px) {
      header {
        align-items: flex-start;
        padding: 22px;
      }
      .terminal {
        margin: 22px;
        font-size: 13px;
      }
      .hint {
        padding: 0 22px 22px;
      }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div class="logo logo-{{.Page.LogoSlug}}" aria-label="{{.Page.DisplayName}} logo">
        {{if eq .Page.LogoSlug "cloudflare"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#f6821f" d="M42.6 42.8H17.1c-4.7 0-8.6-3.8-8.6-8.6 0-4.3 3.2-7.9 7.4-8.5 1.9-7.4 8.6-12.6 16.5-12.6 8.7 0 15.9 6.5 16.9 14.9 3.5.9 6.1 4.1 6.1 7.9 0 3.8-2.6 7-6.2 7.8l-6.6-.9z"/>
          <path fill="#ffb74d" d="M37.8 30.8c1.4-1.2 3.3-1.9 5.3-1.9h6c3.6 0 6.5 2.9 6.5 6.5s-2.9 6.5-6.5 6.5H25.7l12.1-11.1z"/>
        </svg>
        {{else if eq .Page.LogoSlug "linear"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <circle cx="32" cy="32" r="30" fill="#ffffff"/>
          <path fill="#111111" d="M14 41.4 41.4 14c2.2 1.2 4.1 2.8 5.7 4.7L18.7 47.1A25.3 25.3 0 0 1 14 41.4zM12 31.4 31.4 12c2.1 0 4.2.3 6.1.9L12.9 37.5a25 25 0 0 1-.9-6.1zM23.1 51.4l28.3-28.3c.8 2 1.2 4.1 1.2 6.4L29.5 52.6c-2.3 0-4.4-.4-6.4-1.2z"/>
        </svg>
        {{else if eq .Page.LogoSlug "miro"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#111111" d="M16 12h8l6 14 7-14h8l-6 40h-8l3.1-21.7L28 44h-6l-6-31.9z"/>
        </svg>
        {{else if eq .Page.LogoSlug "notion"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <rect x="10" y="8" width="44" height="48" rx="4" fill="#ffffff" stroke="#111111" stroke-width="4"/>
          <path fill="#111111" d="M20 46V18h7.5l10.8 17.1V18H44v28h-6.9L25.7 28.2V46H20z"/>
        </svg>
        {{else if eq .Page.LogoSlug "atlassian"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#ffffff" d="M21.8 47.6c-1.1 0-1.9-1-1.5-2l7.2-18.5c.5-1.4 2.5-1.4 3 0l3.4 8.6-4.2 10.7c-.3.8-1 1.2-1.8 1.2h-6.1z"/>
          <path fill="#ffffff" d="M37.9 47.6c-.8 0-1.5-.5-1.8-1.2L28.6 27c-.5-1.4.5-2.8 1.9-2.8h6c.8 0 1.5.5 1.8 1.2l7.9 20.2c.4 1-.4 2-1.5 2h-6.8z"/>
          <path fill="#ffffff" d="M17.8 18.2c-.9 0-1.6-.7-1.6-1.6V12h31.6v4.6c0 .9-.7 1.6-1.6 1.6H17.8z"/>
        </svg>
        {{else}}
        <span>{{.Page.LogoText}}</span>
        {{end}}
      </div>
      <div>
        <p class="eyebrow">toolmux mcp auth</p>
        <h1>{{.Page.DisplayName}} {{if .Success}}is connected{{else}}authorization failed{{end}}</h1>
      </div>
    </header>
    <section class="terminal" aria-live="polite">
      <div><span class="prompt">$</span> toolmux mcp auth login {{.Page.ServerName}}</div>
      {{if .Success}}
      <div><span class="ok">OK</span> oauth callback received</div>
      <div><span class="ok">OK</span> MCP server link established</div>
      {{else}}
      <div><span class="err">ERR</span> authorization failed</div>
      {{if .Error}}<div class="error-detail">{{.Error}}</div>{{end}}
      {{end}}
      <div><span class="muted">...</span> return to your terminal</div>
    </section>
    {{if .Success}}
    <p class="hint">You can close this window. Toolmux will finish the connection in your terminal and store MCP server auth locally in your OS credential store.</p>
    {{else}}
    <p class="hint">You can close this window and return to your terminal for details. No MCP server token was stored from this callback.</p>
    {{end}}
  </main>
</body>
</html>`))
