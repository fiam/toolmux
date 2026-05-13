package slackauth

// jsExtractTeams reads the per-team xoxc tokens out of Slack's web client. It
// tries several strategies because Slack has moved the data around over the
// years (and a/b-tests new layouts):
//
//  1. localConfig_v2 / localConfig — the modern key shape, used by app.slack.com.
//  2. window.boot_data / TS.boot_data — server-injected globals on legacy
//     <workspace>.slack.com pages.
//  3. Brute-force scan of every localStorage value for an xoxc- token, used
//     when Slack ships a layout we haven't seen yet. Team metadata will be
//     empty in that case but the token (the only piece slack-mcp-server
//     actually needs) is still returned.
//
// Returns [] until at least one xoxc- token is visible, which is the poll
// signal that sign-in completed.
// jsHookRequests is injected as a WKUserScript at document-start. It
// monkey-patches fetch / XHR / WebSocket so any xoxc-… that flies through gets
// stashed on window.__slackauth_captured_token before Slack's app code even
// runs. Slack makes its first API call within ~1s of mount, so the captured
// token is usually visible on the next 1.5s extractor poll.
//
//lint:ignore U1000 used by platform-specific webview backends.
const jsHookRequests = `(() => {
  if (window.__slackauth_hooked) return;
  window.__slackauth_hooked = true;
  const re = /xoxc-[0-9A-Za-z-]+/;
  const apply = (val) => {
    try {
      if (typeof val !== 'string') return;
      const m = val.match(re);
      if (m) window.__slackauth_captured_token = m[0];
    } catch (e) {}
  };
  try {
    const origFetch = window.fetch;
    if (origFetch) {
      window.fetch = function(input, init) {
        try {
          if (typeof input === 'string') apply(input);
          if (init && init.headers) {
            const h = init.headers;
            if (h && typeof h.get === 'function') apply(h.get('Authorization'));
            else if (typeof h === 'object') apply(h.Authorization || h.authorization);
          }
          const body = init && init.body;
          if (typeof body === 'string') apply(body);
          else if (body && typeof FormData !== 'undefined' && body instanceof FormData) apply(body.get('token'));
        } catch (e) {}
        return origFetch.apply(this, arguments);
      };
    }
  } catch (e) {}
  try {
    const proto = XMLHttpRequest.prototype;
    const origSet = proto.setRequestHeader;
    const origSend = proto.send;
    proto.setRequestHeader = function(name, value) {
      try { if (name && name.toLowerCase() === 'authorization') apply(value); } catch (e) {}
      return origSet.apply(this, arguments);
    };
    proto.send = function(body) {
      try {
        if (typeof body === 'string') apply(body);
        else if (body && typeof FormData !== 'undefined' && body instanceof FormData) apply(body.get('token'));
      } catch (e) {}
      return origSend.apply(this, arguments);
    };
  } catch (e) {}
  try {
    const origWS = window.WebSocket;
    if (origWS) {
      const Wrapped = function(url, protocols) {
        try { if (typeof url === 'string') apply(url); } catch (e) {}
        return new origWS(url, protocols);
      };
      Wrapped.prototype = origWS.prototype;
      for (const k of ['CONNECTING','OPEN','CLOSING','CLOSED']) {
        try { Wrapped[k] = origWS[k]; } catch (e) {}
      }
      window.WebSocket = Wrapped;
    }
  } catch (e) {}
})()`

// Scripts below are passed to callAsyncJavaScript: which wraps them as the
// body of an async function — so they should be straight statements ending in
// `return <json-string>`, with no outer IIFE.

//lint:ignore U1000 used by platform-specific webview backends.
const jsExtractTeams = `
  const xoxcRe = /xoxc-[0-9A-Za-z-]+/g;
  const out = [];
  const seen = new Set();
  const push = (token, t) => {
    if (!token || seen.has(token)) return;
    seen.add(token);
    out.push({
      id: (t && (t.id || t.team_id)) || '',
      name: (t && (t.name || t.team_name)) || '',
      domain: (t && (t.domain || t.team_domain)) || '',
      token: token,
    });
  };
  const harvestString = (s, meta) => {
    if (typeof s !== 'string') return;
    const m = s.match(xoxcRe);
    if (m) for (const tok of m) push(tok, meta);
  };

  // 1. Structured localConfig_v2 / localConfig (older shape).
  for (const key of ['localConfig_v2', 'localConfig']) {
    try {
      const raw = localStorage.getItem(key);
      if (!raw) continue;
      const obj = JSON.parse(raw);
      const teams = obj && obj.teams ? Object.values(obj.teams) : [];
      for (const t of teams) {
        if (t && typeof t.token === 'string' && t.token.indexOf('xoxc-') === 0) {
          push(t.token, t);
        }
      }
    } catch (e) {}
  }
  if (out.length > 0) return out;

  // 2. Server-injected boot_data / TS.boot_data globals.
  try {
    const boot = (typeof window !== 'undefined' && (window.boot_data || (window.TS && window.TS.boot_data))) || null;
    if (boot && typeof boot.api_token === 'string' && boot.api_token.indexOf('xoxc-') === 0) {
      push(boot.api_token, {
        id: boot.team_id,
        name: (boot.team && boot.team.name) || boot.team_name,
        domain: (boot.team && boot.team.domain) || boot.team_domain,
      });
    }
  } catch (e) {}
  if (out.length > 0) return out;

  // 3. Brute scan of localStorage + sessionStorage values.
  for (const store of [localStorage, sessionStorage]) {
    try {
      for (let i = 0; i < store.length; i++) {
        harvestString(store.getItem(store.key(i)), null);
      }
    } catch (e) {}
  }
  if (out.length > 0) return out;

  // 4. IndexedDB scan — Slack's modern client moved a lot of state here.
  try {
    if (typeof indexedDB !== 'undefined' && indexedDB.databases) {
      const dbs = await indexedDB.databases();
      for (const dbInfo of dbs) {
        if (!dbInfo.name) continue;
        try {
          await new Promise((resolve) => {
            const req = indexedDB.open(dbInfo.name);
            req.onsuccess = () => {
              const db = req.result;
              const stores = Array.from(db.objectStoreNames);
              if (stores.length === 0) { try { db.close(); } catch(e){} resolve(); return; }
              const tx = db.transaction(stores, 'readonly');
              let pending = stores.length;
              const done = () => { if (--pending === 0) { try { db.close(); } catch(e){} resolve(); } };
              for (const storeName of stores) {
                try {
                  const getReq = tx.objectStore(storeName).getAll();
                  getReq.onsuccess = () => {
                    try { harvestString(JSON.stringify(getReq.result), null); } catch (e) {}
                    done();
                  };
                  getReq.onerror = done;
                } catch (e) { done(); }
              }
            };
            req.onerror = () => resolve();
            req.onblocked = () => resolve();
          });
        } catch (e) {}
        if (out.length > 0) break;
      }
    }
  } catch (e) {}

  // Strategy 5: hook-captured token from jsHookRequests user script.
  try {
    const tok = window.__slackauth_captured_token;
    if (typeof tok === 'string' && tok.indexOf('xoxc-') === 0) push(tok, null);
  } catch (e) {}

  return JSON.stringify(out);
`

// jsDebugInfo returns a compact view of where we are and what we can see, so
// the Go side can print a diagnostic when polling stalls. Returns a JSON
// string to dodge WKWebView's strict result-bridging rules.
//
//lint:ignore U1000 used by platform-specific webview backends.
const jsDebugInfo = `
  const out = {
    url: String(location.href || ''),
    host: String(location.host || ''),
    local_keys: [],
    session_keys: [],
    idb_databases: [],
    has_boot: false,
    globals: [],
  };
  try {
    for (let i = 0; i < localStorage.length; i++) out.local_keys.push(String(localStorage.key(i) || ''));
  } catch (e) {}
  try {
    for (let i = 0; i < sessionStorage.length; i++) out.session_keys.push(String(sessionStorage.key(i) || ''));
  } catch (e) {}
  try {
    out.has_boot = !!(typeof window !== 'undefined' && (window.boot_data || (window.TS && window.TS.boot_data)));
  } catch (e) {}
  try {
    for (const name of ['boot_data', 'TS', 'slack', '_slack', 'SLACK']) {
      if (typeof window !== 'undefined' && window[name] != null) out.globals.push(name);
    }
  } catch (e) {}
  try {
    if (typeof indexedDB !== 'undefined' && indexedDB.databases) {
      const dbs = await indexedDB.databases();
      out.idb_databases = dbs.map(d => String(d.name || '')).filter(Boolean);
    }
  } catch (e) {}
  try { out.has_captured = typeof window.__slackauth_captured_token === 'string'; } catch (e) {}
  return JSON.stringify(out);
`
