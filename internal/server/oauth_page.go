package server

import (
	"bytes"
	"html/template"
	"net/http"
)

type oauthSuccessPage struct {
	Provider oauthProviderPage
}

type oauthProviderPage struct {
	Name string
	Slug string
	Logo string
}

func writeOAuthSuccessPage(w http.ResponseWriter, page oauthSuccessPage) {
	var body bytes.Buffer
	if err := oauthSuccessTemplate.Execute(&body, page); err != nil {
		writeError(w, http.StatusInternalServerError, "render_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

var oauthSuccessTemplate = template.Must(template.New("oauth-success").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Provider.Name}} connected - Toolmux</title>
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
      width: min(720px, calc(100vw - 32px));
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
      width: 54px;
      height: 54px;
      flex: 0 0 auto;
      display: grid;
      place-items: center;
      border-radius: 8px;
      background: #ffffff;
      color: #111111;
      border: 1px solid rgba(255, 255, 255, 0.2);
      font-family: Georgia, "Times New Roman", serif;
      font-size: 34px;
      font-weight: 700;
      line-height: 1;
    }
    .logo svg {
      width: 34px;
      height: 34px;
      display: block;
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
    .muted {
      color: #8ea0b8;
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
      <div class="logo" aria-label="{{.Provider.Name}} logo">
        {{if eq .Provider.Slug "slack"}}
        <svg viewBox="0 0 122.8 122.8" aria-hidden="true" focusable="false">
          <path fill="#E01E5A" d="M25.8 77.6c0 7.1-5.8 12.9-12.9 12.9S0 84.7 0 77.6s5.8-12.9 12.9-12.9h12.9v12.9z"/>
          <path fill="#E01E5A" d="M32.3 77.6c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9v32.3c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V77.6z"/>
          <path fill="#36C5F0" d="M45.2 25.8c-7.1 0-12.9-5.8-12.9-12.9S38.1 0 45.2 0s12.9 5.8 12.9 12.9v12.9H45.2z"/>
          <path fill="#36C5F0" d="M45.2 32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H12.9C5.8 58.1 0 52.3 0 45.2s5.8-12.9 12.9-12.9h32.3z"/>
          <path fill="#2EB67D" d="M97 45.2c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9-5.8 12.9-12.9 12.9H97V45.2z"/>
          <path fill="#2EB67D" d="M90.5 45.2c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V12.9C64.7 5.8 70.5 0 77.6 0s12.9 5.8 12.9 12.9v32.3z"/>
          <path fill="#ECB22E" d="M77.6 97c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9-12.9-5.8-12.9-12.9V97h12.9z"/>
          <path fill="#ECB22E" d="M77.6 90.5c-7.1 0-12.9-5.8-12.9-12.9s5.8-12.9 12.9-12.9h32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H77.6z"/>
        </svg>
        {{else}}
        {{.Provider.Logo}}
        {{end}}
      </div>
      <div>
        <p class="eyebrow">toolmux auth</p>
        <h1>{{.Provider.Name}} is connected</h1>
      </div>
    </header>
    <section class="terminal" aria-live="polite">
      <div><span class="prompt">$</span> toolmux connect {{.Provider.Slug}}</div>
      <div><span class="ok">OK</span> oauth callback received</div>
      <div><span class="ok">OK</span> agent link established</div>
      <div><span class="muted">...</span> return to your terminal</div>
    </section>
    <p class="hint">You can close this window. Toolmux will finish the connection in your terminal and store provider tokens locally in your OS credential store.</p>
  </main>
</body>
</html>`))
