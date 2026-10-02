// Mock data source for the lastcall mocks.
// Stands in for the controller API: GET /api/snapshot plus an event stream at /api/events.
// Routing follows the plan's reset-first rules: per lab pool, usable accounts only,
// operator tier first, then earliest weekly reset (one-minute buckets), then stable id.
(() => {
  const SEC = 1000, MIN = 60 * SEC, HOUR = 60 * MIN, DAY = 24 * HOUR;
  const TICK = 3 * SEC;
  const FRESH_LIMIT = 15 * MIN;
  const HISTORY = 40, TRAIL = 30, EVENTS = 120;
  const t0 = Date.now();

  const LABS = {
    anthropic: { name: 'Anthropic', product: 'Claude', logo: 'anthropic', color: '#cc785c' },
    openai: { name: 'OpenAI', product: 'Codex', logo: 'openai', color: '#f1f3f5' },
    google: { name: 'Google', product: 'Antigravity', logo: 'antigravity', color: '#56ca80' },
    xai: { name: 'xAI', product: 'Grok', logo: 'xai', color: '#9b94ee' },
  };

  const CLIENTS = [
    { id: 'claude-code', name: 'Claude Code', logo: 'claude', labs: ['anthropic'] },
    { id: 'codex-cli', name: 'Codex CLI', logo: 'openai', labs: ['openai'] },
    { id: 'opencode', name: 'opencode', logo: null, labs: ['anthropic', 'openai'] },
    { id: 't3code', name: 'T3 Code', logo: null, labs: ['anthropic', 'openai'] },
  ];

  const MODELS = {
    anthropic: ['claude-opus-5-5', 'claude-opus-5-5', 'claude-sonnet-5-5', 'claude-fable-5-1'],
    openai: ['gpt-5.6-luna', 'gpt-5.6-luna', 'gpt-5.6-terra'],
  };

  // id, lab, plan, label, color, 5h used, 5h resets in, weekly used, weekly resets in, requests, tokens
  // codex-1 starts near its 5h cap so the demo shows a live routing handoff within ~30s.
  const seed = [
    ['claude-1', 'anthropic', 'Claude Max 20×', 'Max · A', '#df9477', 0.42, 2.3 * HOUR, 0.61, 29 * HOUR, 214, 3.1e6],
    ['claude-2', 'anthropic', 'Claude Max 5×', 'Max · B', '#f0b99f', 0.08, 4.6 * HOUR, 0.18, 5 * DAY + 3 * HOUR, 37, 0.6e6],
    ['codex-1', 'openai', 'ChatGPT Pro', 'Pro · A', '#f1f3f5', 0.955, 1.2 * HOUR, 0.74, 14 * HOUR, 388, 5.4e6],
    ['codex-2', 'openai', 'ChatGPT Plus', 'Plus · B', '#c5cbd3', 0.21, 3.9 * HOUR, 0.33, 3 * DAY + 2 * HOUR, 96, 1.2e6],
    ['codex-3', 'openai', 'ChatGPT Plus', 'Plus · C', '#939daa', 0, 5 * HOUR, 1, 2 * DAY + 7 * HOUR, 0, 0],
    ['codex-4', 'openai', 'ChatGPT Plus', 'Plus · D', '#6b7380', 1, 0.7 * HOUR, 0.47, 4 * DAY + 11 * HOUR, 152, 2.0e6],
  ];

  const accounts = seed.map(([id, lab, plan, label, color, sUsed, sIn, wUsed, wIn, requests, tokens]) => ({
    id, lab, plan, label, color, tier: 1,
    short: { used: sUsed, resetAt: t0 + sIn },
    weekly: { used: wUsed, resetAt: t0 + wIn },
    requests, tokens,
    observedAt: t0,
    trail: [],
  }));

  const unmanaged = [
    { id: 'antigravity-1', lab: 'google', label: 'Antigravity' },
    { id: 'xai-1', lab: 'xai', label: 'Grok' },
  ];

  const pools = [...new Set(accounts.map(a => a.lab))];
  const history = Object.fromEntries(pools.map(l => [l, Array.from({ length: HISTORY }, () => Math.floor(Math.random() * 4))]));
  const events = [];
  let seq = 0;
  let lastNext = {};
  let ticks = 0;

  const pick = xs => xs[Math.floor(Math.random() * xs.length)];
  const byId = id => accounts.find(a => a.id === id);

  function skipReason(a, now) {
    if (now - a.observedAt > FRESH_LIMIT) return 'Quota data stale';
    if (a.weekly.used >= 1) return 'Weekly allowance used up';
    if (a.short.used >= 1) return '5h window full';
    return null;
  }

  function buildPlan(now) {
    return pools.map(lab => {
      const usable = [], skipped = [];
      for (const a of accounts.filter(x => x.lab === lab)) {
        const reason = skipReason(a, now);
        if (reason) skipped.push({ id: a.id, reason });
        else usable.push(a);
      }
      usable.sort((x, y) =>
        x.tier - y.tier ||
        Math.floor(x.weekly.resetAt / MIN) - Math.floor(y.weekly.resetAt / MIN) ||
        x.id.localeCompare(y.id));
      return {
        lab,
        frozen: skipped.some(s => s.reason === 'Quota data stale'),
        next: usable[0]?.id ?? null,
        ranked: usable.map((a, i) => ({ id: a.id, rank: i + 1 })),
        skipped,
      };
    });
  }

  function push(e) {
    events.push({ id: ++seq, ...e });
    if (events.length > EVENTS) events.shift();
  }

  function logRouting(now, plan) {
    for (const p of plan) {
      const prev = lastNext[p.lab];
      if (prev === p.next) continue;
      const to = byId(p.next);
      const from = prev ? byId(prev) : null;
      const why = from ? (skipReason(from, now) ?? 'an earlier reset became usable') : 'resets soonest';
      push({
        t: now, kind: 'route', lab: p.lab, from: prev ?? null, to: p.next,
        text: to
          ? `${LABS[p.lab].product} → ${to.label}${from ? `, ${from.label}: ${why}` : `, ${why}`}. Weekly resets in ${fmt.duration(to.weekly.resetAt - now)}.`
          : `${LABS[p.lab].product}: no usable account, CLIProxyAPI falls back to native routing.`,
      });
      lastNext[p.lab] = p.next;
    }
  }

  // One simulated poll + traffic step. Traffic lands on each pool's top-ranked account,
  // which is what CLIProxyAPI does once the controller has written priorities.
  function tick(now) {
    ticks++;
    for (const a of accounts) {
      if (now >= a.short.resetAt) { a.short.used = 0; a.short.resetAt += 5 * HOUR; }
      if (now >= a.weekly.resetAt) { a.weekly.used = 0; a.weekly.resetAt += 7 * DAY; }
      a.observedAt = now;
    }
    const plan = buildPlan(now);
    logRouting(now, plan);

    const batch = [];
    for (const p of plan) {
      const a = byId(p.next);
      const reqs = a ? 1 + Math.floor(Math.random() * 4) : 0;
      for (let i = 0; i < reqs; i++) {
        const out = Math.round(400 + Math.random() * 4000);
        const inp = Math.round(6000 + Math.random() * 30000);
        a.requests++;
        a.tokens += inp + out;
        a.short.used = Math.min(1, a.short.used + 0.0015);
        a.weekly.used = Math.min(1, a.weekly.used + 0.0006);
        batch.push({
          t: now - Math.random() * TICK, kind: 'req', lab: p.lab, account: a.id,
          client: pick(CLIENTS.filter(c => c.labs.includes(p.lab))).id,
          model: pick(MODELS[p.lab]), in: inp, out, ms: Math.round(900 + Math.random() * 7000),
        });
      }
      const h = history[p.lab];
      h.push(reqs);
      if (h.length > HISTORY) h.shift();
    }
    batch.sort((x, y) => x.t - y.t).forEach(push);

    if (ticks % 10 === 1) {
      push({ t: now, kind: 'poll', text: `Polled quota for ${accounts.length} accounts, ${plan.filter(p => !p.frozen).length}/${plan.length} pools fresh. Priorities unchanged.` });
    }
    for (const a of accounts) {
      a.trail.push({ t: now, left: 1 - a.weekly.used, resetIn: a.weekly.resetAt - now });
      if (a.trail.length > TRAIL) a.trail.shift();
    }
    logRouting(now, buildPlan(now));
  }

  function snapshot(now) {
    const plan = buildPlan(now);
    const route = new Map();
    for (const p of plan) {
      for (const r of p.ranked) route.set(r.id, { rank: r.rank, next: r.id === p.next, reason: null });
      for (const s of p.skipped) route.set(s.id, { rank: null, next: false, reason: s.reason });
    }
    return structuredClone({
      generatedAt: now,
      labs: LABS, clients: CLIENTS,
      accounts: accounts.map(a => ({ ...a, route: route.get(a.id) })),
      unmanaged, plan, history, events,
    });
  }

  function fetchSnapshot() {
    return new Promise(resolve => setTimeout(() => {
      const now = Date.now();
      tick(now);
      resolve(snapshot(now));
    }, 300 + Math.random() * 400));
  }

  const listeners = new Set();
  let timer = null;
  function subscribe(fn) {
    listeners.add(fn);
    timer ??= setInterval(() => {
      const now = Date.now();
      tick(now);
      const s = snapshot(now);
      for (const l of listeners) l(s);
    }, TICK);
    return () => listeners.delete(fn);
  }

  const fmt = {
    duration(ms) {
      if (ms <= 0) return 'now';
      const d = Math.floor(ms / DAY), h = Math.floor(ms % DAY / HOUR), m = Math.floor(ms % HOUR / MIN);
      if (d) return `${d}d ${h}h`;
      if (h) return `${h}h ${m}m`;
      return m ? `${m}m` : '<1m';
    },
    ago(ms) {
      if (ms < 5 * SEC) return 'just now';
      if (ms < MIN) return `${Math.floor(ms / SEC)}s ago`;
      return `${Math.floor(ms / MIN)}m ago`;
    },
    clock: t => new Date(t).toLocaleTimeString('en-GB', { hour12: false }),
    pct: x => `${Math.round(x * 100)}%`,
    tokens(n) {
      if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
      if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}k`;
      return String(n);
    },
    num: n => n.toLocaleString('en-US'),
  };

  const logo = (key, cls = 'logo') => key && window.LOGOS?.[key]
    ? `<svg class="${cls}" viewBox="0 0 24 24" aria-hidden="true">${window.LOGOS[key]}</svg>` : '';

  const TABS = [['burn.html', 'Burn map'], ['console.html', 'Console'], ['flow.html', 'Flow']];
  const REFRESH_ICON = '<svg viewBox="0 0 16 16"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5v3h-3"/></svg>';

  // Shared top bar. Pages without live data (the index) pass live: false.
  function chrome(active, { live = true } = {}) {
    const el = document.getElementById('bar');
    el.className = 'bar';
    el.innerHTML = `<a class="mark" href="index.html">last<span>call</span></a>
      <nav class="tabs">${TABS.map(([href, name]) => `<a href="${href}"${href === active ? ' aria-current="page"' : ''}>${name}</a>`).join('')}</nav>
      ${live ? `<div class="live" data-live><span class="dot"></span><span data-live-text>Connecting…</span>
        <button class="refresh" data-refresh title="Fetch now">${REFRESH_ICON}Refresh</button></div>` : ''}`;
  }

  // Wires a page: fetch on load, live updates from the stream, a manual refresh button,
  // and a once-a-second redraw so countdowns and "updated Xs ago" stay current.
  function mount({ render }) {
    const live = document.querySelector('[data-live]');
    const text = live.querySelector('[data-live-text]');
    const button = document.querySelector('[data-refresh]');
    let snap = null, busy = false;

    const draw = () => {
      live.classList.toggle('busy', busy);
      button.disabled = busy;
      if (busy) text.textContent = 'Fetching…';
      if (!snap) return;
      if (!busy) text.textContent = `Live · updated ${fmt.ago(Date.now() - snap.generatedAt)}`;
      render(snap, Date.now());
    };
    const refresh = async () => {
      busy = true; draw();
      snap = await fetchSnapshot();
      busy = false; draw();
    };

    button.addEventListener('click', refresh);
    subscribe(s => { snap = s; draw(); });
    setInterval(draw, SEC);
    refresh();
  }

  window.lastcall = { LABS, fmt, logo, chrome, fetchSnapshot, subscribe, mount };
})();
