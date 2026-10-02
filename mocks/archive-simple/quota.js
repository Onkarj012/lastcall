// Mock of the data on CPAMC's Quota Management page (127.0.0.1:8317/management.html#/quota).
// Values and fields mirror the 2026-10-02 18:05 capture; account names are anonymised.
// The real build reads the same per-credential quota through the management API.
(() => {
  const SEC = 1000, MIN = 60 * SEC, HOUR = 60 * MIN, DAY = 24 * HOUR;
  const POLL = 5 * SEC;
  const t0 = Date.now();
  const at = ms => t0 + ms;

  const PROVIDERS = {
    claude: { name: 'Claude', logo: 'claude', color: '#d97757' },
    codex: { name: 'Codex', logo: 'openai', color: '#e8eaed' },
    antigravity: { name: 'Antigravity', logo: 'antigravity', color: '#5b8def' },
    xai: { name: 'xAI', logo: 'xai', color: '#e8eaed' },
  };

  // `left` is the fraction remaining, as CPAMC shows it. resetAt null = no active window.
  const creds = [
    { id: 'claude-a', provider: 'claude', name: 'claude · A', plan: 'Pro',
      meta: [['Resets remaining', '1']],
      limits: [
        { name: '5-hour', left: 0.98, resetAt: at(3 * HOUR + 5 * MIN) },
        { name: '7-day', left: 0.89, resetAt: at(5 * DAY + 3 * HOUR + 25 * MIN) },
        { name: '7-day Fable 5', left: 1, resetAt: at(33 * DAY + 19 * HOUR) },
      ] },
    { id: 'claude-b', provider: 'claude', name: 'claude · B', plan: 'Pro',
      meta: [['Resets remaining', '1']],
      limits: [
        { name: '5-hour', left: 1, resetAt: null },
        { name: '7-day', left: 0.86, resetAt: at(4 * DAY + 18 * HOUR + 25 * MIN) },
        { name: '7-day Fable 5', left: 1, resetAt: at(33 * DAY + 19 * HOUR) },
      ] },
    { id: 'codex-a', provider: 'codex', name: 'codex · A', plan: 'Plus',
      meta: [['Renews', 'in 10 days'], ['Credits', '0'], ['Manual resets', '2']],
      limits: [
        { name: '5-hour', left: 0.71, resetAt: at(2 * HOUR + 56 * MIN) },
        { name: 'Weekly', left: 0.80, resetAt: at(6 * DAY + 16 * HOUR + 49 * MIN) },
      ] },
    { id: 'codex-b', provider: 'codex', name: 'codex · B', plan: 'Plus',
      meta: [['Renews', 'in 17 days'], ['Credits', '0'], ['Manual resets', '2']],
      limits: [
        { name: '5-hour', left: 0.78, resetAt: at(2 * HOUR + 56 * MIN) },
        { name: 'Weekly', left: 0.81, resetAt: at(6 * DAY + 16 * HOUR + 49 * MIN) },
      ] },
    { id: 'antigravity-a', provider: 'antigravity', name: 'antigravity · A', plan: 'Free',
      meta: [],
      limits: [
        { name: 'Gemini models', left: 1, resetAt: at(7 * DAY) },
        { name: 'Claude + GPT models', left: 1, resetAt: at(7 * DAY) },
      ] },
    { id: 'xai-a', provider: 'xai', name: 'xai · A', plan: 'X Premium',
      meta: [['Pay as you go', 'Disabled']],
      limits: [
        { name: 'Weekly', left: 0, resetAt: at(51 * MIN) },
        { name: 'GrokBuild', left: 0, resetAt: null },
      ] },
  ];

  // Stand-in traffic: the two accounts in active use drain a little each poll.
  function poll(now) {
    for (const c of creds) {
      for (const l of c.limits) {
        if (l.resetAt && now >= l.resetAt) { l.left = 1; l.resetAt = null; }
      }
    }
    for (const id of ['claude-a', 'codex-a']) {
      for (const l of creds.find(c => c.id === id).limits.slice(0, 2)) {
        l.left = Math.max(0, l.left - Math.random() * (l.name === '5-hour' ? 0.004 : 0.0015));
      }
    }
  }

  const snapshot = now => structuredClone({ fetchedAt: now, providers: PROVIDERS, creds });

  function fetchQuota() {
    return new Promise(resolve => setTimeout(() => {
      const now = Date.now();
      poll(now);
      resolve(snapshot(now));
    }, 400 + Math.random() * 500));
  }

  const fmt = {
    pct: x => `${Math.round(x * 100)}%`,
    in(ms) {
      if (ms == null) return '';
      if (ms <= 0) return 'now';
      const d = Math.floor(ms / DAY), h = Math.floor(ms % DAY / HOUR), m = Math.floor(ms % HOUR / MIN);
      if (d) return h ? `${d}d ${h}h` : `${d}d`;
      if (h) return `${h}h ${m}m`;
      return m ? `${m}m` : '<1m';
    },
    when: t => t == null ? '' : new Date(t).toLocaleString('en-US', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).replace(',', ''),
    ago(ms) {
      if (ms < 5 * SEC) return 'just now';
      if (ms < MIN) return `${Math.floor(ms / SEC)}s ago`;
      return `${Math.floor(ms / MIN)}m ago`;
    },
  };

  // Green when there's room, amber when getting low, red when out.
  const level = left => left <= 0.05 ? 'out' : left < 0.3 ? 'low' : 'ok';

  const logo = (key, color, size = 16) => window.LOGOS?.[key]
    ? `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="${color}" style="flex:none">${window.LOGOS[key]}</svg>` : '';

  // Fetch on open, poll in the background, redraw every second for countdowns,
  // and let the [data-refresh] button force a fetch.
  function mount(render) {
    const status = document.querySelector('[data-status]');
    const button = document.querySelector('[data-refresh]');
    let snap = null, busy = false;

    const draw = () => {
      button.disabled = busy;
      button.classList.toggle('busy', busy);
      if (busy && !snap) status.textContent = 'Fetching quota…';
      if (!snap) return;
      status.textContent = busy ? 'Fetching…' : `Updated ${fmt.ago(Date.now() - snap.fetchedAt)}`;
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

  window.quota = { fmt, level, logo, mount };
})();
