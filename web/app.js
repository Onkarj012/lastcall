// lastcall UI. Reads /api/snapshot from the lastcall backend; never talks to CPA directly.
(() => {
  const MIN = 60e3, HOUR = 60 * MIN, DAY = 24 * HOUR;
  const POLL = 5000;

  const PROVIDERS = {
    claude: { name: 'Claude', logo: 'claude', color: '#cc785c', shades: ['#df9477', '#f0b99f', '#cc785c', '#b8644a'] },
    codex: { name: 'Codex', logo: 'openai', color: '#f1f3f5', shades: ['#f1f3f5', '#939daa', '#c5cbd3', '#6b7380'] },
    antigravity: { name: 'Antigravity', logo: 'antigravity', color: '#56ca80', shades: ['#56ca80', '#8fdcaa'] },
    xai: { name: 'Grok', logo: 'xai', color: '#9b94ee', shades: ['#9b94ee', '#c3beff'] },
  };
  const LOGIN = [['claude', 'Claude'], ['codex', 'Codex'], ['antigravity', 'Antigravity'], ['xai', 'Grok']];
  const prov = id => PROVIDERS[id] ?? { name: id, logo: null, color: '#aaa39d', shades: ['#aaa39d'] };

  const $ = id => document.getElementById(id);
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const PIN = on => `<svg viewBox="0 0 16 16" fill="${on ? 'currentColor' : 'none'}" stroke="currentColor" stroke-width="1.4"><path d="M10.5 1.5l4 4-2 1-2.5 2.5.5 3-1.5 1.5-3-3-3.5 3.5-.5-.5L5.5 10l-3-3L4 5.5l3 .5L9.5 3.5z"/></svg>`;
  const logo = (key, color, size = 14) => key && window.LOGOS?.[key]
    ? `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="${color}" style="flex:none">${LOGOS[key]}</svg>` : '';

  const fmt = {
    in(ms) {
      if (ms == null) return '—';
      if (ms <= 0) return 'now';
      const d = Math.floor(ms / DAY), h = Math.floor(ms % DAY / HOUR), m = Math.floor(ms % HOUR / MIN);
      if (d) return h ? `${d}d ${h}h` : `${d}d`;
      if (h) return `${h}h ${m}m`;
      return m ? `${m}m` : '<1m';
    },
    ago(ms) {
      if (ms < 10e3) return 'just now';
      if (ms < MIN) return `${Math.floor(ms / 1000)}s ago`;
      if (ms < HOUR) return `${Math.floor(ms / MIN)}m ago`;
      return `${Math.floor(ms / HOUR)}h ago`;
    },
    pct: x => `${Math.round(x * 100)}%`,
    clock: t => new Date(t).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }),
    time: t => new Date(t).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false }),
  };
  const until = (t, now) => t ? fmt.in(new Date(t) - now) : '—';
  const state = left => left <= 0.02 ? 'out' : left < 0.2 ? 'low' : '';
  const short = label => esc(String(label).split('@')[0]);

  // ---- API ----
  async function api(method, path, body) {
    const res = await fetch(path, {
      method, headers: { 'Content-Type': 'application/json', 'X-Lastcall': '1' },
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    return data;
  }

  let snap = null, openMenu = null, busy = false;

  async function load() {
    try {
      snap = await api('GET', '/api/snapshot');
      draw();
    } catch (e) {
      $('dot').className = 'dot bad';
      $('status').textContent = 'lastcall backend unreachable';
    }
  }

  function toast(msg, bad = false) {
    const el = document.createElement('div');
    el.className = `toast ${bad ? 'bad' : ''}`;
    el.textContent = msg;
    document.body.append(el);
    setTimeout(() => el.remove(), bad ? 6000 : 2600);
  }

  async function run(label, fn) {
    if (busy) return;
    busy = true;
    try {
      await fn();
      if (label) toast(label);
    } catch (e) {
      toast(e.message, true);
    } finally {
      busy = false;
      await load();
    }
  }

  // ---- derived ----
  const shadeOf = a => {
    const p = prov(a.provider);
    const i = snap.accounts.filter(x => x.provider === a.provider).indexOf(a);
    return p.shades[i % p.shades.length];
  };

  // Primary windows on a card: the first 5h and first weekly limit; everything else is "extra".
  function split(a) {
    const ws = a.quota?.windows ?? [];
    const five = ws.filter(w => w.kind === '5h');
    const week = ws.filter(w => w.kind === 'week');
    const main = [];
    if (five.length) main.push(five.reduce((x, y) => (y.left < x.left ? y : x)));
    if (week.length) main.push(week[0]);
    if (!main.length) main.push(...ws.slice(0, 2));
    return { main, extra: ws.filter(w => !main.includes(w)) };
  }

  // Sum the same-named limit across a provider's accounts: two accounts make a 200% pool.
  function pool(provider) {
    const accts = snap.accounts.filter(a => a.provider === provider);
    const pick = kind => {
      const first = accts.map(a => split(a).main.find(w => w.kind === kind)).find(Boolean);
      if (!first) return null;
      const name = kind === '5h' ? null : first.name;
      const parts = accts.map(a => {
        const w = (a.quota?.windows ?? []).filter(x => x.kind === kind && (!name || x.name === name))
          .reduce((x, y) => (!x || y.left < x.left ? y : x), null);
        return w && { a, left: w.left, reset: w.reset_at, off: a.disabled };
      }).filter(Boolean);
      if (!parts.length) return null;
      const resets = parts.map(p => p.reset).filter(Boolean).map(t => new Date(t)).filter(t => t > Date.now());
      return { kind, parts, left: parts.reduce((n, p) => n + (p.off ? 0 : p.left), 0), cap: parts.length, next: resets.length ? Math.min(...resets) : null };
    };
    return { accts, five: pick('5h'), week: pick('week') };
  }

  const seg = (parts, h) => `<div class="seg">${parts.map(p =>
    `<span class="${p.off ? 'off' : ''}" style="height:${h}px">${p.off ? '' : `<i style="width:${p.left * 100}%;background:${state(p.left) === 'out' ? 'var(--out)' : shadeOf(p.a)}"></i>`}</span>`).join('')}</div>`;

  // ---- render ----
  function draw() {
    const now = Date.now();
    drawStatus(now);
    drawTotals(now);
    drawSubs(now);
    drawActivity();
    drawRouting();
    $('stamp').textContent = `${fmt.clock(snap.now)} · ${snap.accounts.length} credentials · quota ${snap.quota_at && new Date(snap.quota_at).getFullYear() > 2000 ? fmt.time(snap.quota_at) : 'pending'}`;
  }

  function drawStatus(now) {
    const qAt = new Date(snap.quota_at);
    const hasQuota = qAt.getFullYear() > 2000;
    let dot = 'dot', text;
    if (snap.error) { dot = 'dot bad'; text = 'Not connected'; }
    else if (snap.refreshing) { dot = 'dot warn'; text = 'Fetching quota…'; }
    else text = hasQuota ? `Live · quota ${fmt.ago(now - qAt)} · polls every ${Math.round(snap.quota_every_s / 60)}m` : 'Waiting for first quota sweep…';
    $('dot').className = dot;
    $('status').textContent = text;
    $('refresh').classList.toggle('spin', snap.refreshing);
    $('banner').innerHTML = !snap.error ? '' : /no management key|empty/.test(snap.error)
      ? `lastcall needs your CPA management key. Save it to <code>~/.config/lastcall/management-key</code> (chmod 600); it's picked up automatically.`
      : /rejected|banned|invalid/.test(snap.error)
        ? `CPA rejected the management key, so lastcall stopped calling it (5 bad tries locks localhost out for 30 min). Fix the key file and it retries. <span class="mono">${esc(snap.error)}</span>`
        : `Can't reach CLIProxyAPI: <span class="mono">${esc(snap.error)}</span>`;
  }

  function drawTotals(now) {
    const present = [...new Set(snap.accounts.map(a => a.provider))];
    const shown = snap.pins.filter(p => present.includes(p));
    $('totals').style.setProperty('--n', Math.max(1, shown.length));
    $('totals').innerHTML = shown.map(id => {
      const p = prov(id), pl = pool(id);
      const main = pl.five ?? pl.week, side = pl.five ? pl.week : null;
      const plan = pl.accts.map(a => a.plan).filter(Boolean);
      const sub = pl.accts.length > 1 ? `${pl.accts.length} accounts combined${plan.length ? ` · ${[...new Set(plan)].join(', ')}` : ''}` : (plan[0] ?? '1 account');
      const big = (w, cls) => `<div class="n ${cls} ${w.left / w.cap <= 0.02 ? 'out' : ''}"><b>${Math.round(w.left * 100)}%</b><span>/ ${w.cap * 100}%</span></div>`;
      return `<div class="total" style="--c:${p.color}">
        <div class="who">${logo(p.logo, p.color, 18)}${p.name}<small>${esc(sub)}</small>
          <button class="pin on" data-pin="${id}" title="Unpin">${PIN(true)}</button></div>
        ${main ? `<div class="row2">
          <div><div class="k">${main.kind === '5h' ? '5-hour' : 'Weekly'}</div>${big(main, '')}${seg(main.parts, 7)}
            <div class="when">${main.next ? `next reset in <b>${fmt.in(main.next - now)}</b>` : 'idle'}</div></div>
          ${side ? `<div><div class="k">Weekly</div>${big(side, 'sm')}${seg(side.parts, 4)}
            <div class="when">next reset in <b>${side.next ? fmt.in(side.next - now) : '—'}</b></div></div>` : '<div></div>'}
        </div>` : `<div class="when" style="margin-top:14px">No quota yet.</div>`}
      </div>`;
    }).join('');
  }

  function badges(a, now) {
    const b = [];
    if (a.pause) b.push(`<span class="badge warn">paused · back ${fmt.time(a.pause.resume_at)}</span>`);
    else if (a.disabled) b.push(`<span class="badge bad">disabled</span>`);
    if (a.unavailable && !a.disabled) b.push(`<span class="badge warn">cooling${a.next_retry_after ? ` · ${until(a.next_retry_after, now)}` : ''}</span>`);
    if (a.status === 'error' && a.status_message) b.push(`<span class="badge bad" title="${esc(a.status_message)}">error</span>`);
    if (a.route?.rank === 1 && snap.routing.applied) b.push(`<span class="badge route">routing first</span>`);
    else if (a.route?.rank) b.push(`<span class="badge route">${snap.routing.applied ? '' : 'suggested '}#${a.route.rank}</span>`);
    if (a.priority) b.push(`<span class="badge" title="CPA priority">prio ${a.priority}</span>`);
    return b.join('');
  }

  function drawSubs(now) {
    const order = [...snap.accounts].sort((x, y) => (snap.pins.includes(y.provider) - snap.pins.includes(x.provider)));
    $('subs').innerHTML = order.map(a => {
      const p = prov(a.provider), shade = shadeOf(a), pinned = snap.pins.includes(a.provider);
      const { main, extra } = split(a);
      const q = a.quota;
      const meta = [
        ...extra.map(w => `${esc(w.name)} ${fmt.pct(w.left)}`),
        ...(q?.meta ?? []).map(([k, v]) => `${esc(k)} ${esc(v)}`),
        `${a.success.toLocaleString()} ok · ${a.failed} failed`,
      ];
      return `<div class="sub ${pinned ? '' : 'dim'} ${a.disabled ? 'off' : ''}" data-name="${esc(a.name)}">
        <div class="top"><div class="who">${logo(p.logo, a.disabled ? '#77716c' : shade, 15)}${p.name} · ${short(a.label)}<small>${esc(a.plan ?? '')}</small></div>
          <button class="pin ${pinned ? 'on' : ''}" data-pin="${a.provider}" title="${pinned ? 'Unpin' : 'Pin'} ${p.name}">${PIN(pinned)}</button>
          <button class="more" data-menu="${esc(a.name)}" title="Actions">⋯</button></div>
        <div class="badges">${badges(a, now)}</div>
        ${!a.supported ? `<div class="meta">quota not supported for ${esc(a.provider)}</div>`
          : !q ? `<div class="meta">${a.disabled ? 'disabled; quota not fetched' : 'quota pending…'}</div>`
          : main.map(w => `<div class="lim"><div class="t"><span>${esc(w.name)}</span><span class="v ${state(w.left)}">${fmt.pct(w.left)}<em>${until(w.reset_at, now)}</em></span></div>
              <div class="track"><i style="width:${w.left * 100}%;background:${state(w.left) === 'out' ? 'var(--out)' : shade}"></i></div></div>`).join('')}
        ${q?.error ? `<div class="qerr" title="${esc(q.error)}">quota: ${esc(q.error.slice(0, 140))}</div>` : ''}
        <div class="meta">${meta.join(' · ')}${q?.at ? ` · read ${fmt.ago(now - new Date(q.at))}` : ''}</div>
        ${openMenu === a.name ? menu(a) : ''}
      </div>`;
    }).join('') || `<div class="meta">No credentials yet.</div>`;
  }

  function menu(a) {
    const five = (a.quota?.windows ?? []).some(w => w.kind === '5h' && w.reset_at && new Date(w.reset_at) > Date.now());
    const week = (a.quota?.windows ?? []).some(w => w.kind === 'week' && w.reset_at && new Date(w.reset_at) > Date.now());
    const n = esc(a.name);
    return `<div class="menu">
      ${a.pause ? `<button data-act="resume" data-name="${n}">Resume now</button>` : ''}
      <button data-act="pause" data-min="30" data-name="${n}" ${a.disabled ? 'disabled' : ''}>Pause 30 min</button>
      <button data-act="pause" data-min="120" data-name="${n}" ${a.disabled ? 'disabled' : ''}>Pause 2 hours</button>
      <button data-act="pause" data-until="5h" data-name="${n}" ${a.disabled || !five ? 'disabled' : ''}>Pause until 5-hour reset</button>
      <button data-act="pause" data-until="week" data-name="${n}" ${a.disabled || !week ? 'disabled' : ''}>Pause until weekly reset</button>
      <hr>
      ${a.disabled ? `<button data-act="enable" data-name="${n}">Enable</button>` : `<button class="danger" data-act="disable" data-name="${n}">Disable</button>`}
      <button data-act="relogin" data-provider="${esc(a.provider)}">Re-login ${prov(a.provider).name}…</button>
    </div>`;
  }

  // Requests per 10-minute bucket over the last 200 minutes, from CPA's own counters.
  function drawActivity() {
    const by = {};
    let ok = 0, failed = 0, life = 0, lifeFail = 0;
    for (const a of snap.accounts) {
      life += a.success; lifeFail += a.failed;
      const r = a.recent ?? [];
      by[a.provider] ??= Array(r.length).fill(0);
      r.forEach((b, i) => { by[a.provider][i] = (by[a.provider][i] ?? 0) + b.success + b.failed; ok += b.success; failed += b.failed; });
    }
    $('stats').innerHTML = `<div><b>${(ok + failed).toLocaleString()}</b><span>requests · 200 min</span></div>
      <div><b>${failed}</b><span>failed · 200 min</span></div>
      <div><b>${life ? ((life / (life + lifeFail)) * 100).toFixed(1) : '—'}%</b><span>success since CPA start</span></div>`;

    const svg = $('spark'), W = svg.clientWidth || 500, H = 64, R = 70;
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    const series = Object.entries(by).filter(([, xs]) => xs.length > 1);
    const max = Math.max(4, ...series.flatMap(([, xs]) => xs));
    let g = '', last = -Infinity;
    const ends = [];
    for (const [k, xs] of series) {
      const p = prov(k), dim = !snap.pins.includes(k);
      const pts = xs.map((v, i) => [(i / (xs.length - 1)) * (W - R), (H - 6) * (1 - v / max) + 3]);
      g += `<polyline points="${pts.map(q => q.join(',')).join(' ')}" fill="none" stroke="${dim ? '#36312d' : p.color}" stroke-width="1.4"/>`;
      if (!dim) ends.push({ p, y: pts.at(-1)[1], n: xs.reduce((s, v) => s + v, 0) });
    }
    ends.sort((a, b) => a.y - b.y);
    for (const e of ends) {
      const y = Math.min(H - 4, Math.max(e.y, last + 13)); last = y;
      g += `<svg x="${W - R + 8}" y="${y - 6}" width="11" height="11" viewBox="0 0 24 24" fill="${e.p.color}">${LOGOS[e.p.logo] ?? ''}</svg>
        <text x="${W - R + 23}" y="${y + 4}" fill="${e.p.color}" font-family="DM Sans" font-size="11" font-weight="600">${e.n}</text>`;
    }
    svg.innerHTML = g;
  }

  function drawRouting() {
    const r = snap.routing;
    const lbl = name => short(snap.accounts.find(a => a.name === name)?.label ?? name);
    const pools = r.pools.map(pl => {
      const p = prov(pl.provider);
      if (pl.frozen) return `<div class="pool">${p.name}: <span class="low">paused, ${esc(pl.frozen)}</span></div>`;
      const ranked = pl.slots.filter(s => s.rank).map(s => lbl(s.name));
      const skipped = pl.slots.filter(s => !s.rank).map(s => `${lbl(s.name)} (${esc(s.reason)})`);
      return `<div class="pool" title="${esc(skipped.join(', '))}">${p.name}: ${ranked.join(' → ') || 'nothing usable'}${skipped.length ? ` <span class="mute">· skip ${skipped.join(', ')}</span>` : ''}</div>`;
    }).join('');
    $('route').innerHTML = `<div class="hd"><b>Reset-first routing</b>
        <span class="toggle ${r.auto ? 'on' : ''}" data-auto="${r.auto ? 0 : 1}" title="Re-apply after every quota sweep"><i></i>auto</span>
        <span class="btns"><button class="btn primary" data-route="apply">Apply</button>${r.applied ? '<button class="btn" data-route="restore">Restore</button>' : ''}</span></div>
      ${pools || '<div class="pool">No managed providers.</div>'}
      <div class="note">${r.error ? `<span class="out">${esc(r.error)}</span>` : r.applied ? 'Priorities written to CPA. Restore puts back the originals.' : 'Dry run: nothing written to CPA until you press Apply.'}</div>`;
  }

  // ---- login modal ----
  function loginModal(provider) {
    const scrim = document.createElement('div');
    scrim.className = 'scrim';
    let state = null, timer = null;
    const close = () => { clearInterval(timer); scrim.remove(); load(); };
    const pickView = () => `<div class="modal"><h3>Log in an account</h3>
      <div class="provs">${LOGIN.map(([id, n]) => `<button data-login="${id}">${logo(prov(id).logo, prov(id).color, 16)}${n}</button>`).join('')}</div>
      <div class="mute" style="font-size:12px">Opens the provider's login in a new tab. CPA stores the credential; logging in an account it already has updates it in place.</div>
      <div class="row"><button class="btn" data-close>Cancel</button></div></div>`;
    const start = async id => {
      try {
        const s = await api('POST', '/api/oauth/start', { provider: id });
        state = s.state;
        if (s.url) window.open(s.url, '_blank', 'noopener');
        scrim.innerHTML = `<div class="modal"><h3>${prov(id).name} login</h3>
          ${s.user_code ? `<div>Enter this code on the page that opened:</div><div class="code">${esc(s.user_code)}</div>` : '<div>Finish signing in on the tab that opened.</div>'}
          ${s.url ? `<div style="font-size:12px">Tab didn't open? <a href="${esc(s.url)}" target="_blank" rel="noopener">Open login page</a></div>` : ''}
          <div class="mute" id="lstatus">Waiting for the provider…</div>
          <div class="row"><button class="btn" data-close>Close</button></div></div>`;
        timer = setInterval(async () => {
          try {
            const st = await fetch(`/api/oauth/status?state=${encodeURIComponent(state)}`).then(r => r.json());
            if (st.status === 'ok') { clearInterval(timer); toast(`${prov(id).name} account saved`); close(); }
            else if (st.status === 'error') { clearInterval(timer); scrim.querySelector('#lstatus').innerHTML = `<span class="out">${esc(st.error || 'login failed')}</span>`; }
          } catch { /* keep polling */ }
        }, 2000);
      } catch (e) {
        toast(e.message, true);
        close();
      }
    };
    scrim.addEventListener('click', e => {
      if (e.target === scrim || e.target.closest('[data-close]')) close();
      const b = e.target.closest('[data-login]');
      if (b) start(b.dataset.login);
    });
    scrim.innerHTML = pickView();
    document.body.append(scrim);
    if (provider) start(provider);
  }

  // ---- events ----
  document.addEventListener('click', e => {
    const t = e.target;
    const pin = t.closest('[data-pin]');
    if (pin) return run(null, () => api('POST', '/api/pins', { provider: pin.dataset.pin, pinned: !snap.pins.includes(pin.dataset.pin) }));
    const m = t.closest('[data-menu]');
    if (m) { openMenu = openMenu === m.dataset.menu ? null : m.dataset.menu; return draw(); }
    const act = t.closest('[data-act]');
    if (act) {
      openMenu = null;
      const name = act.dataset.name, path = `/api/accounts/${encodeURIComponent(name)}/`;
      switch (act.dataset.act) {
        case 'pause': {
          const body = act.dataset.min ? { minutes: +act.dataset.min } : { until: act.dataset.until };
          return run('Paused', () => api('POST', path + 'pause', body));
        }
        case 'disable':
          if (!confirm('Disable this account in CLIProxyAPI? It stays off until you enable it.')) return draw();
          return run('Disabled', () => api('POST', path + 'disable'));
        case 'enable': return run('Enabled', () => api('POST', path + 'enable'));
        case 'resume': return run('Resumed', () => api('POST', path + 'resume'));
        case 'relogin': draw(); return loginModal(act.dataset.provider);
      }
    }
    const rt = t.closest('[data-route]');
    if (rt) {
      if (rt.dataset.route === 'apply') return run('Priorities applied', () => api('POST', '/api/routing/apply'));
      return run('Original priorities restored', () => api('POST', '/api/routing/restore'));
    }
    const auto = t.closest('[data-auto]');
    if (auto) return run(auto.dataset.auto === '1' ? 'Auto routing on' : 'Auto routing off', () => api('POST', '/api/routing/auto', { on: auto.dataset.auto === '1' }));
    if (openMenu && !t.closest('.menu')) { openMenu = null; draw(); }
  });
  $('refresh').addEventListener('click', () => run('Quota refreshed', () => api('POST', '/api/refresh')));
  $('add').addEventListener('click', () => loginModal());

  load();
  setInterval(load, POLL);
})();
