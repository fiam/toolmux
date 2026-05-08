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

func notionOAuthProviderPage() oauthProviderPage {
	return oauthProviderPage{
		Name: "Notion",
		Slug: "notion",
		Logo: "N",
	}
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
  <title>{{.Provider.Name}} connected - Supacli</title>
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
      <div class="logo" aria-label="{{.Provider.Name}} logo">{{.Provider.Logo}}</div>
      <div>
        <p class="eyebrow">supacli auth</p>
        <h1>{{.Provider.Name}} is connected</h1>
      </div>
    </header>
    <section class="terminal" aria-live="polite">
      <div><span class="prompt">$</span> supacli connect {{.Provider.Slug}}</div>
      <div><span class="ok">OK</span> oauth callback received</div>
      <div><span class="ok">OK</span> agent link established</div>
      <div><span class="muted">...</span> return to your terminal</div>
    </section>
    <p class="hint">You can close this window. Supacli will finish the connection in your terminal and store provider tokens locally in your OS credential store.</p>
  </main>
</body>
</html>`))
