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
    day: t => new Date(t).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }),
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

  // Every card shows exactly two limit rows: the tightest 5-hour window, then weekly
  // (or monthly) ones. Model-specific extras stay off the card. Missing rows are null.
  function slots(a) {
    const ws = a.quota?.windows ?? [];
    const five = ws.filter(w => w.kind === '5h').reduce((x, y) => (!x || y.left < x.left ? y : x), null);
    const out = [five, ...ws.filter(w => w.kind === 'week'), ...ws.filter(w => w.kind === 'month')].filter(Boolean);
    return [out[0] ?? null, out[1] ?? null];
  }

  // Sum the same-named limit across a provider's accounts: two accounts make a 200% pool.
  function pool(provider) {
    const accts = snap.accounts.filter(a => a.provider === provider);
    const pick = kind => {
      const first = accts.map(a => slots(a).find(w => w?.kind === kind)).find(Boolean);
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

  // One status badge per card, most important state wins.
  function status(a, now) {
    if (a.pause) return `<span class="badge warn">paused · back ${fmt.time(a.pause.resume_at)}</span>`;
    if (a.disabled) return `<span class="badge bad">disabled</span>`;
    if (a.unavailable) return `<span class="badge warn">cooling${a.next_retry_after ? ` · ${until(a.next_retry_after, now)}` : ''}</span>`;
    if (a.status === 'error') return `<span class="badge bad" title="${esc(a.status_message)}">error</span>`;
    if (a.quota?.error) return `<span class="badge warn" title="${esc(a.quota.error)}">quota stale</span>`;
    return `<span class="badge ok">active</span>`;
  }

  // Lay provider groups into columns (each group stays in one column, shortest column
  // first) and give every card an explicit cell, so all rows share one height.
  function layout(groups) {
    const width = $('subs').clientWidth || 1200;
    const n = Math.max(1, Math.min(groups.length, Math.floor((width + 14) / (360 + 14))));
    const heights = Array(n).fill(0), cells = [];
    for (const g of groups) {
      const c = heights.indexOf(Math.min(...heights));
      g.forEach((a, i) => cells.push({ a, col: c + 1, row: heights[c] + i + 1 }));
      heights[c] += g.length;
    }
    return { n, cells };
  }

  function limRow(w, shade, now) {
    if (!w) return `<div class="lim empty"><div class="t"><span>—</span><span class="v"></span></div><div class="track"></div></div>`;
    return `<div class="lim"><div class="t"><span>${esc(w.name)}</span><span class="v ${state(w.left)}">${fmt.pct(w.left)}<em>${until(w.reset_at, now)}</em></span></div>
      <div class="track"><i style="width:${w.left * 100}%;background:${state(w.left) === 'out' ? 'var(--out)' : shade}"></i></div></div>`;
  }

  function drawSubs(now) {
    const order = [...snap.accounts].sort((x, y) => (snap.pins.includes(y.provider) - snap.pins.includes(x.provider)));
    const groups = [...new Set(order.map(a => a.provider))].map(id => order.filter(a => a.provider === id));
    const { n, cells } = layout(groups);
    $('subs').style.setProperty('--cols', n);
    $('subs').innerHTML = cells.map(({ a, col, row }) => {
      const p = prov(a.provider), shade = shadeOf(a), pinned = snap.pins.includes(a.provider);
      const q = a.quota, rs = q?.resets;
      const renews = (q?.meta ?? []).find(([k]) => k === 'renews');
      const foot = [renews && `renews ${esc(renews[1])}`, q?.at ? `read ${fmt.ago(now - new Date(q.at))}` : !a.supported ? 'quota not supported' : a.disabled ? 'not read while disabled' : 'quota pending…'].filter(Boolean);
      return `<div class="sub ${pinned ? '' : 'dim'} ${a.disabled ? 'off' : ''}" style="grid-column:${col};grid-row:${row}" data-name="${esc(a.name)}">
        <div class="top"><div class="who">${logo(p.logo, a.disabled ? '#77716c' : shade, 15)}${p.name} · ${short(a.label)}<small>${esc(a.plan ?? '')}</small></div>
          <button class="pin ${pinned ? 'on' : ''}" data-pin="${a.provider}" title="${pinned ? 'Unpin' : 'Pin'} ${p.name}">${PIN(pinned)}</button>
          <button class="more" data-menu="${esc(a.name)}" title="Actions">⋯</button></div>
        <div class="badges">${status(a, now)}</div>
        ${slots(a).map(w => limRow(w, shade, now)).join('')}
        <div class="foot">
          ${rs ? `<button class="rbtn" data-resets="${esc(a.name)}">↺ ${rs.left} reset${rs.left === 1 ? '' : 's'}</button>` : `<span class="rnone">no banked resets</span>`}
          <span class="meta">${foot.join(' · ')}</span>
        </div>
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

  // ---- resets modal: every banked reset with its own expiry, and the button to spend one ----
  function resetsModal(name) {
    const a = snap.accounts.find(x => x.name === name), rs = a?.quota?.resets;
    if (!rs) return;
    const p = prov(a.provider), now = Date.now();
    const scrim = document.createElement('div');
    scrim.className = 'scrim';
    scrim.innerHTML = `<div class="modal" style="--c:${shadeOf(a)}"><h3>${logo(p.logo, shadeOf(a), 16)} ${p.name} · ${short(a.label)} · ${rs.left} banked reset${rs.left === 1 ? '' : 's'}</h3>
      <div class="rlist">${rs.items.map(it => `<div class="ritem"><div><b>${esc(it.title || 'Usage-limit reset')}</b>${it.count > 1 ? ` <span class="mute">×${it.count}</span>` : ''}</div>
        <div class="mono">${it.expires_at ? `expires ${fmt.clock(it.expires_at)} · in ${until(it.expires_at, now)}` : 'no expiry given'}</div></div>`).join('') || `<div class="mute">The provider reports ${rs.left} but didn't list them.</div>`}</div>
      <div class="mute" style="font-size:12.5px">${rs.clears ? `Using one clears your ${esc(rs.clears)} limits right away. ` : ''}${rs.note ? `<span class="out">${esc(rs.note)}</span>` : ''}</div>
      <div class="row"><button class="btn" data-close>Close</button><button class="btn primary" data-use ${rs.usable ? '' : 'disabled'}>Use one reset</button></div></div>`;
    const close = () => scrim.remove();
    scrim.addEventListener('click', e => {
      if (e.target === scrim || e.target.closest('[data-close]')) return close();
      if (e.target.closest('[data-use]')) {
        if (!confirm(`Spend 1 of ${rs.left} banked resets on ${p.name} · ${a.label}? This can't be undone.`)) return;
        close();
        run(null, async () => toast((await api('POST', `/api/accounts/${encodeURIComponent(name)}/reset`)).message || 'Limits reset'));
      }
    });
    document.body.append(scrim);
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
    const rb = t.closest('[data-resets]');
    if (rb) return resetsModal(rb.dataset.resets);
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
    if (openMenu && !t.closest('.menu')) { openMenu = null; draw(); }
  });
  $('refresh').addEventListener('click', () => run('Quota refreshed', () => api('POST', '/api/refresh')));
  $('add').addEventListener('click', () => loginModal());

  addEventListener('resize', () => snap && drawSubs(Date.now()));
  load();
  setInterval(load, POLL);
})();
