// Analytics view: API-equivalent cost of everything the local clients sent.
(() => {
  const SRC = {
    'claude-code': { name: 'Claude Code', color: '#cc785c', logo: 'claude' },
    codex: { name: 'Codex', color: '#f1f3f5', logo: 'openai' },
    opencode: { name: 'opencode', color: '#52cdd2', logo: 'opencode' },
  };
  // Monthly list prices of the plans lastcall can see, for the value comparison.
  const PLAN_USD = { claude: { Pro: 20, Max: 100, Team: 30 }, codex: { Plus: 20, Pro: 200, Team: 30 }, opencode: { Go: 10, 'Go Plus': 40 } };

  const $ = id => document.getElementById(id);
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const logo = (key, color, size = 13) => key && window.LOGOS?.[key] ? `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="${color}" style="flex:none">${LOGOS[key]}</svg>` : '';
  const usd = n => n >= 1000 ? `$${(n / 1000).toFixed(1)}k` : n >= 100 ? `$${n.toFixed(0)}` : `$${n.toFixed(2)}`;
  const tok = n => n >= 1e9 ? `${(n / 1e9).toFixed(2)}B` : n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(0)}k` : String(n);
  const num = n => n.toLocaleString('en-US');
  const DAYS = { '24h': 1, '7d': 7, '30d': 30, '60d': 60 };

  const st = { range: '7d', source: '', model: '', project: '', sort: {}, data: null, loading: false, at: 0 };

  async function load() {
    st.loading = true; draw();
    const q = new URLSearchParams({ range: st.range, source: st.source, model: st.model, project: st.project });
    try {
      const r = await fetch(`/api/analytics?${q}`);
      st.data = await r.json();
      st.at = Date.now();
    } catch { /* keep last data */ }
    st.loading = false; draw();
  }

  function subsCost(snap) {
    const per = [];
    for (const a of snap?.accounts ?? []) {
      const price = PLAN_USD[a.provider]?.[a.plan];
      if (price) per.push({ name: `${a.provider === 'codex' ? 'ChatGPT' : a.provider === 'claude' ? 'Claude' : 'OpenCode'} ${a.plan}`, usd: price });
    }
    return per;
  }

  // Sortable table. cols: [key, label, fmt, align]
  function table(id, rows, cols, opts = {}) {
    const s = st.sort[id] ?? { key: 'cost', dir: -1 };
    const val = (r, k) => k === 'per' ? (r.cost / Math.max(1, r.requests)) : k === 'tokens' ? r.in + r.cache_read + r.cache_write + r.out : r[k];
    const sorted = [...rows].sort((a, b) => {
      const x = val(a, s.key), y = val(b, s.key);
      return (typeof x === 'string' ? x.localeCompare(y) : x - y) * s.dir;
    }).slice(0, opts.limit ?? 50);
    const max = Math.max(...rows.map(r => r.cost), 1e-9);
    return `<table class="at"><thead><tr>${cols.map(([k, l, , al]) =>
      `<th data-sort="${id}:${k}" class="${al ?? ''} ${s.key === k ? 'on' : ''}">${l}${s.key === k ? (s.dir < 0 ? ' ↓' : ' ↑') : ''}</th>`).join('')}</tr></thead>
      <tbody>${sorted.map(r => `<tr>${cols.map(([k, , f, al]) => `<td class="${al ?? ''}">${f(r, max)}</td>`).join('')}</tr>`).join('')}</tbody></table>`;
  }

  const costCell = (r, max) => `<span class="cbar"><i style="width:${(r.cost / max) * 100}%;background:${SRC[r.source]?.color ?? 'var(--brand)'}"></i></span><b>${usd(r.cost)}</b>`;
  const keyCell = (r) => `${r.source ? logo(SRC[r.source]?.logo, SRC[r.source]?.color) : ''}<span>${esc(r.key)}</span>${r.unpriced ? `<em class="low" title="${r.unpriced} responses have no known API price">${r.unpriced} unpriced</em>` : ''}`;
  const COLS = [
    ['requests', 'Requests', r => num(r.requests), 'r'],
    ['sessions', 'Sessions', r => num(r.sessions), 'r'],
    ['in', 'Input', r => tok(r.in), 'r'],
    ['cache_read', 'Cache read', r => tok(r.cache_read), 'r'],
    ['cache_write', 'Cache write', r => tok(r.cache_write), 'r'],
    ['out', 'Output', r => tok(r.out), 'r'],
    ['per', '$ / req', r => `$${(r.cost / Math.max(1, r.requests)).toFixed(3)}`, 'r'],
    ['cost', 'API cost', costCell, 'r wide'],
  ];

  function chart(d) {
    const W = 1000, H = 170, n = d.buckets.length, gap = n > 40 ? 1 : 3;
    const max = Math.max(...d.buckets.map(b => b.total), 1e-9);
    const bw = W / n;
    let g = '';
    d.buckets.forEach((b, i) => {
      let y = H;
      for (const k of Object.keys(SRC)) {
        const v = b.by[k] ?? 0;
        if (!v) continue;
        const h = (v / max) * (H - 18);
        y -= h;
        g += `<rect x="${i * bw + gap / 2}" y="${y}" width="${bw - gap}" height="${h}" fill="${SRC[k].color}" opacity="${k === 'codex' ? .85 : 1}"><title>${new Date(b.start).toLocaleString('en-US', d.bucket_size === 'hour' ? { hour: '2-digit', hour12: false, month: 'short', day: 'numeric' } : { weekday: 'short', month: 'short', day: 'numeric' })} · ${SRC[k].name} ${usd(v)}</title></rect>`;
      }
    });
    const labels = d.buckets.map((b, i) => [b, i]).filter(([, i]) => i % Math.ceil(n / 8) === 0)
      .map(([b, i]) => `<text x="${i * bw + bw / 2}" y="${H + 16}" text-anchor="middle">${new Date(b.start).toLocaleString('en-US', d.bucket_size === 'hour' ? { hour: '2-digit', hour12: false } : { month: 'short', day: 'numeric' })}</text>`).join('');
    const peak = d.buckets.reduce((a, b) => (b.total > a.total ? b : a), d.buckets[0]);
    return `<svg class="achart" viewBox="0 0 ${W} ${H + 22}" preserveAspectRatio="none">
      <line x1="0" x2="${W}" y1="18" y2="18" stroke="#3d3833" stroke-dasharray="2 4"/><text x="${W}" y="13" text-anchor="end">${usd(max)}</text>
      ${g}${labels}</svg>
      <div class="legend">${Object.entries(SRC).map(([, s]) => `<span><i style="background:${s.color}"></i>${s.name}</span>`).join('')}
        <span class="mute">peak ${usd(peak.total)} · ${new Date(peak.start).toLocaleString('en-US', d.bucket_size === 'hour' ? { hour: '2-digit', hour12: false, month: 'short', day: 'numeric' } : { weekday: 'short', month: 'short', day: 'numeric' })}</span></div>`;
  }

  const strip = (vals, labels) => {
    const max = Math.max(...vals, 1e-9);
    return `<div class="strip">${vals.map((v, i) => `<div title="${labels[i]} · ${usd(v)}"><i style="height:${Math.max(2, (v / max) * 100)}%;opacity:${0.35 + 0.65 * v / max}"></i><span>${labels[i]}</span></div>`).join('')}</div>`;
  };

  function draw() {
    const root = $('v-analytics');
    if (root.hidden) return;
    const d = st.data;
    const select = (key, label, opts) => `<select data-f="${key}"><option value="">${label}: all</option>${opts.map(o => `<option ${st[key] === o ? 'selected' : ''} value="${esc(o)}">${esc(key === 'source' ? SRC[o]?.name ?? o : o)}</option>`).join('')}</select>`;
    let h = `<div class="actl">
      <div class="pills">${Object.keys(DAYS).map(r => `<button data-range="${r}" class="${st.range === r ? 'on' : ''}">${r === '24h' ? 'Past 24 hours' : `Past ${DAYS[r]} days`}</button>`).join('')}</div>
      ${d ? `${select('source', 'Client', d.options.Sources)}${select('model', 'Model', d.options.Models)}${select('project', 'Project', d.options.Projects)}` : ''}
      <span class="mute">${st.loading ? 'Loading…' : d ? `scanned ${new Date(d.scanned_at).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })}` : ''}</span></div>`;
    if (!d || !d.totals) { root.innerHTML = h + `<div class="apanel mute">Reading local history… the first scan takes about 15 seconds.</div>`; return; }

    const t = d.totals, days = DAYS[d.range];
    const subs = subsCost(window.lastcallSnap);
    const subMonthly = subs.reduce((n, s) => n + s.usd, 0);
    const subPeriod = subMonthly * days / 30;
    const allTok = t.in + t.cache_read + t.cache_write + t.out;
    const cacheShare = (t.cache_read) / Math.max(1, t.in + t.cache_read + t.cache_write);
    h += `<div class="kpis">
      <div class="kpi hero"><span>API-equivalent cost</span><b>${usd(t.cost)}</b><small>${num(t.requests)} responses priced at public API rates${t.unpriced ? ` · <span class="low">${num(t.unpriced)} unpriced</span>` : ''}</small></div>
      <div class="kpi"><span>Subscriptions, same period</span><b>${usd(subPeriod)}</b><small>${subs.length ? `${usd(subMonthly)}/mo · ${subs.map(s => s.name).join(' + ')}` : 'no plans detected'}</small></div>
      <div class="kpi"><span>Value multiple</span><b class="brand">${subPeriod ? `${(t.cost / subPeriod).toFixed(1)}×` : '—'}</b><small>API cost ÷ what the plans cost</small></div>
      <div class="kpi"><span>Tokens</span><b>${tok(allTok)}</b><small>${tok(t.out)} out · ${(cacheShare * 100).toFixed(0)}% of input from cache</small></div>
      <div class="kpi"><span>Sessions</span><b>${num(d.sessions)}</b><small>${d.active_days} active day${d.active_days === 1 ? '' : 's'} · ${usd(t.cost / Math.max(1, d.active_days))}/active day</small></div>
    </div>
    <div class="apanel"><div class="h">${d.bucket_size === 'hour' ? 'Per hour' : 'Per day'} <small>API-equivalent cost by client</small></div>${chart(d)}</div>
    <div class="agrid2">
      <div class="apanel"><div class="h">Time of day <small>local time</small></div>${strip(d.hours, d.hours.map((_, i) => String(i).padStart(2, '0')))}</div>
      <div class="apanel"><div class="h">Day of week</div>${strip([...d.weekdays.slice(1), d.weekdays[0]], ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'])}</div>
    </div>
    <div class="apanel"><div class="h">Models <small>click a column to sort</small></div>${table('models', d.models, [['key', 'Model', keyCell], ...COLS])}</div>
    <div class="agrid2">
      <div class="apanel"><div class="h">Projects</div>${table('projects', d.projects, [['key', 'Project', keyCell], ['requests', 'Requests', r => num(r.requests), 'r'], ['sessions', 'Sessions', r => num(r.sessions), 'r'], ['cost', 'API cost', costCell, 'r wide']], { limit: 15 })}</div>
      <div class="apanel"><div class="h">Reasoning effort</div>${table('efforts', d.efforts, [['key', 'Effort', keyCell], ['requests', 'Requests', r => num(r.requests), 'r'], ['out', 'Output', r => tok(r.out), 'r'], ['per', '$ / req', r => `$${(r.cost / Math.max(1, r.requests)).toFixed(3)}`, 'r'], ['cost', 'API cost', costCell, 'r wide']])}
        <div class="h" style="margin-top:22px">Clients</div>${table('clients', d.clients, [['key', 'Client', keyCell], ['requests', 'Requests', r => num(r.requests), 'r'], ['cost', 'API cost', costCell, 'r wide']])}</div>
    </div>
    <div class="apanel"><div class="h">Most expensive sessions</div>${table('top', d.top_sessions.map(s => ({ ...s, key: s.project })), [
      ['start', 'Started', r => new Date(r.start).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })],
      ['key', 'Project', r => `${logo(SRC[r.source]?.logo, SRC[r.source]?.color)}<span>${esc(r.key)}</span>`],
      ['model', 'Main model', r => `<span class="mono">${esc(r.model)}</span>`],
      ['requests', 'Requests', r => num(r.requests), 'r'],
      ['tokens', 'Tokens', r => tok(r.in + r.cache_read + r.cache_write + r.out), 'r'],
      ['cost', 'API cost', costCell, 'r wide']])}</div>
    <p class="anote">Built from local history: Claude Code transcripts, Codex sessions and opencode's database on this Mac. Other clients and other machines aren't counted. Prices are public API list rates (Anthropic, OpenAI; opencode's own per-response cost), so this is what the same tokens would have cost on pay-as-you-go, not what you paid.</p>`;
    root.innerHTML = h;
  }

  document.addEventListener('click', e => {
    const r = e.target.closest('[data-range]');
    if (r) { st.range = r.dataset.range; return load(); }
    const s = e.target.closest('[data-sort]');
    if (s) {
      const [id, key] = s.dataset.sort.split(':');
      const cur = st.sort[id] ?? { key: 'cost', dir: -1 };
      st.sort[id] = { key, dir: cur.key === key ? -cur.dir : (key === 'key' || key === 'model' ? 1 : -1) };
      return draw();
    }
  });
  document.addEventListener('change', e => {
    const f = e.target.closest('[data-f]');
    if (f) { st[f.dataset.f] = f.value; load(); }
  });

  window.analytics = {
    show() { if (!st.data || Date.now() - st.at > 60e3) load(); else draw(); },
  };
  setInterval(() => { if (!$('v-analytics').hidden) load(); }, 120e3);
})();
