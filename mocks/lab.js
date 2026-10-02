// Mock data for the lastcall quota page.
// Quota mirrors CPAMC's Quota Management page (2026-10-02 18:05 capture), names anonymised.
// Ongoing usage stands in for CLIProxyAPI's usage stats. The real build reads both through
// the management API; this file is the only thing to swap.
(() => {
  const SEC = 1000, MIN = 60 * SEC, HOUR = 60 * MIN, DAY = 24 * HOUR;
  const POLL = 5 * SEC;
  const t0 = Date.now();
  const at = ms => t0 + ms;

  const PROVIDERS = {
    claude: { name: 'Claude', lab: 'Anthropic', logo: 'claude', color: '#cc785c', shades: ['#df9477', '#f0b99f'] },
    codex: { name: 'Codex', lab: 'OpenAI', logo: 'openai', color: '#f1f3f5', shades: ['#f1f3f5', '#939daa'] },
    antigravity: { name: 'Antigravity', lab: 'Google', logo: 'antigravity', color: '#56ca80', shades: ['#56ca80'] },
    xai: { name: 'Grok', lab: 'xAI', logo: 'xai', color: '#9b94ee', shades: ['#9b94ee'] },
    opencode: { name: 'opencode', lab: 'OpenCode Go', logo: 'opencode', color: '#52cdd2', shades: ['#52cdd2'] },
  };

  // `left` is the fraction remaining, as CPAMC shows it. kind: '5h' | 'week' | 'extra'.
  // `cap` (USD) marks dollar-denominated windows; OpenCode Go meters spend, not requests.
  const creds = [
    { id: 'claude-a', provider: 'claude', name: 'A', plan: 'Pro', meta: [['resets left', '1']], limits: [
      { name: '5-hour', kind: '5h', left: 0.98, resetAt: at(3 * HOUR + 5 * MIN) },
      { name: '7-day', kind: 'week', left: 0.89, resetAt: at(5 * DAY + 3 * HOUR + 25 * MIN) },
      { name: 'Fable 5 · 7-day', kind: 'extra', left: 1, resetAt: at(33 * DAY + 19 * HOUR) },
    ] },
    { id: 'claude-b', provider: 'claude', name: 'B', plan: 'Pro', meta: [['resets left', '1']], limits: [
      { name: '5-hour', kind: '5h', left: 1, resetAt: null },
      { name: '7-day', kind: 'week', left: 0.86, resetAt: at(4 * DAY + 18 * HOUR + 25 * MIN) },
      { name: 'Fable 5 · 7-day', kind: 'extra', left: 1, resetAt: at(33 * DAY + 19 * HOUR) },
    ] },
    { id: 'codex-a', provider: 'codex', name: 'A', plan: 'Plus', meta: [['manual resets', '2'], ['renews', '10/12']], limits: [
      { name: '5-hour', kind: '5h', left: 0.71, resetAt: at(2 * HOUR + 56 * MIN) },
      { name: 'Weekly', kind: 'week', left: 0.80, resetAt: at(6 * DAY + 16 * HOUR + 49 * MIN) },
    ] },
    { id: 'codex-b', provider: 'codex', name: 'B', plan: 'Plus', meta: [['manual resets', '2'], ['renews', '10/19']], limits: [
      { name: '5-hour', kind: '5h', left: 0.78, resetAt: at(2 * HOUR + 56 * MIN) },
      { name: 'Weekly', kind: 'week', left: 0.81, resetAt: at(6 * DAY + 16 * HOUR + 49 * MIN) },
    ] },
    { id: 'antigravity-a', provider: 'antigravity', name: 'A', plan: 'Free', meta: [], limits: [
      { name: '5-hour · Gemini', kind: '5h', left: 0.64, resetAt: at(2 * HOUR + 10 * MIN) },
      { name: '5-hour · Claude + GPT', kind: '5h', left: 0.82, resetAt: at(3 * HOUR + 40 * MIN) },
      { name: 'Weekly', kind: 'week', left: 1, resetAt: at(7 * DAY) },
    ] },
    { id: 'xai-a', provider: 'xai', name: 'A', plan: 'X Premium', meta: [['pay as you go', 'off']], limits: [
      { name: 'Weekly', kind: 'week', left: 0, resetAt: at(51 * MIN) },
      { name: 'GrokBuild', kind: 'extra', left: 0, resetAt: null },
    ] },
    // OpenCode Go is outside CLIProxyAPI; the real build needs opencode's own usage source.
    { id: 'opencode-a', provider: 'opencode', name: 'A', plan: 'Go', meta: [['billing', 'monthly']], limits: [
      { name: '5-hour', kind: '5h', left: 0.66, cap: 12, resetAt: at(1 * HOUR + 35 * MIN) },
      { name: 'Weekly', kind: 'week', left: 0.39, cap: 30, resetAt: at(3 * DAY + 6 * HOUR) },
      { name: 'Monthly', kind: 'extra', left: 0.32, cap: 60, resetAt: at(11 * DAY + 4 * HOUR) },
    ] },
  ];

  const MODELS = [
    ['claude', 'claude-opus-5-5', 0.5], ['claude', 'claude-sonnet-5-5', 0.3], ['claude', 'claude-fable-5-1', 0.2],
    ['codex', 'gpt-5.6-luna', 0.7], ['codex', 'gpt-5.6-terra', 0.3],
    ['antigravity', 'gemini-4-argon', 0.6], ['antigravity', 'claude-sonnet-5-5', 0.4],
    ['opencode', 'glm-5.2', 0.4], ['opencode', 'kimi-k2.6', 0.35], ['opencode', 'qwen3.7-max', 0.25],
  ];
  // Requests per minute, last 60 minutes, per provider. xAI is out of quota, so flat.
  const BASE = { claude: 5, codex: 4, antigravity: 1.5, xai: 0, opencode: 3 };
  const perMin = Object.fromEntries(Object.keys(PROVIDERS).map(p => [p,
    Array.from({ length: 60 }, (_, i) => Math.max(0, Math.round(BASE[p] * (0.6 + 0.8 * Math.abs(Math.sin(i / 7 + p.length))) + (Math.random() - 0.5) * 2)))]));
  const models = MODELS.map(([provider, model, share]) => ({ provider, model, requests: Math.round(share * 900 * (BASE[provider] / 5)), tokens: 0 }));
  for (const m of models) m.tokens = m.requests * (14000 + Math.round(Math.random() * 9000));
  const today = { failed: 7 };
  let minuteStart = t0;

  // Stand-in traffic: accounts in use drain a little each poll, usage counters climb.
  function poll(now) {
    for (const c of creds) for (const l of c.limits) {
      if (l.resetAt && now >= l.resetAt) { l.left = 1; l.resetAt = null; }
    }
    const drain = (id, name, by) => {
      const l = creds.find(c => c.id === id).limits.find(x => x.name === name);
      l.left = Math.max(0, l.left - Math.random() * by);
      if (l.kind === '5h' && !l.resetAt) l.resetAt = now + 5 * HOUR;
    };
    drain('claude-a', '5-hour', 0.004); drain('claude-a', '7-day', 0.0012);
    drain('codex-a', '5-hour', 0.004); drain('codex-a', 'Weekly', 0.0012);
    drain('antigravity-a', '5-hour · Gemini', 0.003);
    drain('opencode-a', '5-hour', 0.004); drain('opencode-a', 'Weekly', 0.0016); drain('opencode-a', 'Monthly', 0.0008);

    if (now - minuteStart >= MIN) {
      for (const p of Object.keys(perMin)) { perMin[p].shift(); perMin[p].push(0); }
      minuteStart = now;
    }
    for (const p of Object.keys(perMin)) {
      const n = Math.random() < BASE[p] / 12 ? 1 + Math.floor(Math.random() * 2) : 0;
      perMin[p][59] += n;
      for (let i = 0; i < n; i++) {
        const pick = models.filter(m => m.provider === p);
        const m = pick[Math.floor(Math.random() * pick.length)];
        m.requests++;
        m.tokens += 8000 + Math.round(Math.random() * 20000);
      }
    }
  }

  // Sum each named limit across a provider's accounts: two Claude accounts make a 200% pool.
  function pools() {
    return Object.keys(PROVIDERS).map(provider => {
      const accts = creds.filter(c => c.provider === provider);
      const names = [...new Set(accts.flatMap(c => c.limits.map(l => l.name)))];
      return {
        provider, accounts: accts.length,
        limits: names.map(name => {
          const parts = accts.map((c, i) => {
            const l = c.limits.find(x => x.name === name);
            return l && { id: c.id, name: c.name, left: l.left, cap: l.cap, resetAt: l.resetAt, shade: PROVIDERS[provider].shades[i] ?? PROVIDERS[provider].color };
          }).filter(Boolean);
          const kind = accts.flatMap(c => c.limits).find(l => l.name === name).kind;
          const resets = parts.map(p => p.resetAt).filter(Boolean);
          const cap = parts.every(p => p.cap) ? parts.reduce((n, p) => n + p.cap, 0) : null;
          return { name, kind, parts, cap, left: parts.reduce((n, p) => n + p.left, 0), capacity: parts.length, nextReset: resets.length ? Math.min(...resets) : null };
        }),
      };
    });
  }

  function snapshot(now) {
    const reqs = models.reduce((n, m) => n + m.requests, 0);
    return structuredClone({
      fetchedAt: now, providers: PROVIDERS, creds, pools: pools(),
      usage: {
        perMin, models: [...models].sort((a, b) => b.tokens - a.tokens),
        requests: reqs, tokens: models.reduce((n, m) => n + m.tokens, 0),
        failed: today.failed, successRate: 1 - today.failed / Math.max(1, reqs),
        lastHour: Object.fromEntries(Object.entries(perMin).map(([p, xs]) => [p, xs.reduce((n, v) => n + v, 0)])),
      },
    });
  }

  function fetchQuota() {
    return new Promise(resolve => setTimeout(() => {
      const now = Date.now();
      poll(now);
      resolve(snapshot(now));
    }, 350 + Math.random() * 450));
  }

  const fmt = {
    pct: x => `${Math.round(x * 100)}%`,
    in(ms) {
      if (ms == null) return '—';
      if (ms <= 0) return 'now';
      const d = Math.floor(ms / DAY), h = Math.floor(ms % DAY / HOUR), m = Math.floor(ms % HOUR / MIN);
      if (d) return h ? `${d}d ${h}h` : `${d}d`;
      if (h) return `${h}h ${m}m`;
      return m ? `${m}m` : '<1m';
    },
    ago(ms) {
      if (ms < 5 * SEC) return 'just now';
      if (ms < MIN) return `${Math.floor(ms / SEC)}s ago`;
      return `${Math.floor(ms / MIN)}m ago`;
    },
    tokens(n) {
      if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
      if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
      if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
      return String(n);
    },
    num: n => n.toLocaleString('en-US'),
    usd: n => `$${n % 1 ? n.toFixed(2) : n}`,
    clock: t => new Date(t).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }),
  };

  // Out of quota stands out; everything else keeps its lab colour.
  const state = left => left <= 0.02 ? 'out' : left < 0.2 ? 'low' : 'ok';

  const logo = (key, color, size = 14) => window.LOGOS?.[key]
    ? `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="${color}" style="flex:none">${window.LOGOS[key]}</svg>` : '';

  const TABS = [['pools.html', 'Pools'], ['pools-a.html', 'A · Refined'], ['pools-b.html', 'B · Twin'], ['pools-c.html', 'C · Grid']];
  const ICON = '<svg viewBox="0 0 16 16"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5v3h-3"/></svg>';

  function chrome(active, { live = true, tabs = TABS } = {}) {
    const el = document.getElementById('bar');
    el.className = 'bar';
    el.innerHTML = `<a class="mark" href="index.html">last<span>call</span></a>
      <nav class="tabs"><a class="dim">Quota</a>${tabs.length ? '<span class="sep"></span>' : ''}${tabs.map(([h, n]) => `<a href="${h}"${h === active ? ' aria-current="page"' : ''}>${n}</a>`).join('')}</nav>
      ${live ? `<div class="live"><span class="dot"></span><span data-status>Fetching…</span><button class="refresh" data-refresh title="Refresh now">${ICON}</button></div>` : ''}`;
  }

  // Fetch on open, poll every 5s, redraw every second for countdowns, Refresh forces a fetch.
  function mount(render) {
    const status = document.querySelector('[data-status]');
    const button = document.querySelector('[data-refresh]');
    let snap = null, busy = false;
    const draw = () => {
      button.disabled = busy;
      document.body.classList.toggle('fetching', busy);
      if (!snap) return;
      status.textContent = busy ? 'Fetching…' : `Live · ${fmt.ago(Date.now() - snap.fetchedAt)}`;
      render(snap, Date.now());
    };
    const refresh = async () => {
      if (busy) return;
      busy = true; draw();
      snap = await fetchQuota();
      busy = false; draw();
    };
    button.addEventListener('click', refresh);
    setInterval(refresh, POLL);
    setInterval(draw, SEC);
    refresh();
  }

  window.lc = { fmt, state, logo, chrome, mount };
})();
