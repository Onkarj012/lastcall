// Drawing helpers shared by the Pools versions.
(() => {
  const { fmt, state, logo } = lc;

  const shadeOf = (s, c) => {
    const p = s.providers[c.provider];
    const i = s.creds.filter(x => x.provider === c.provider).indexOf(c);
    return p.shades[i] ?? p.color;
  };

  // One segment per account; each segment is that account's 100%.
  const seg = (parts, h = 6) => `<div class="seg">${parts.map(p =>
    `<span style="height:${h}px"><i style="width:${p.left * 100}%;background:${p.shade}"></i></span>`).join('')}</div>`;

  // "175/200%" for percentage pools, "$7.92 / $12" for dollar-metered ones.
  const amount = l => l.cap
    ? `${fmt.usd(+(l.left * l.cap / l.capacity).toFixed(2))} / ${fmt.usd(l.cap)}`
    : `${Math.round(l.left * 100)}/${l.capacity * 100}%`;

  const reset = (t, now) => t ? fmt.in(t - now) : '—';

  // Requests per minute, one line per provider, labelled at the line ends.
  function spark(svg, s, { height = 130, right = 92, fill = false, only = null } = {}) {
    const u = s.usage, W = svg.clientWidth || 500, H = height, R = right;
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    const max = Math.max(4, ...Object.values(u.perMin).flat());
    let g = [0, .5, 1].map(f => `<line x1="0" x2="${W - R}" y1="${(H - 8) * (1 - f) + 4}" y2="${(H - 8) * (1 - f) + 4}" stroke="#36312d" stroke-dasharray="1 3"/>`).join('');
    const ends = [];
    for (const [k, xs] of Object.entries(u.perMin)) {
      const p = s.providers[k];
      const pts = xs.map((v, i) => [(i / (xs.length - 1)) * (W - R), (H - 8) * (1 - v / max) + 4]);
      const line = pts.map(q => q.join(',')).join(' ');
      if (fill) g += `<polygon points="0,${H} ${line} ${W - R},${H}" fill="${p.color}" fill-opacity=".05"/>`;
      const dim = only && !only.includes(k);
      g += `<polyline points="${line}" fill="none" stroke="${dim ? '#36312d' : p.color}" stroke-width="1.4" stroke-opacity="${u.lastHour[k] && !dim ? .9 : .5}"/>`;
      if (!dim) ends.push({ p, y: pts.at(-1)[1], n: u.lastHour[k] });
    }
    ends.sort((a, b) => a.y - b.y);
    let last = -Infinity;
    for (const e of ends) {
      const y = Math.min(H - 4, Math.max(e.y, last + 14)); last = y;
      g += `<line x1="${W - R}" x2="${W - R + 8}" y1="${e.y}" y2="${y}" stroke="#36312d"/>
        <svg x="${W - R + 11}" y="${y - 6}" width="12" height="12" viewBox="0 0 24 24" fill="${e.p.color}">${LOGOS[e.p.logo]}</svg>
        <text x="${W - R + 28}" y="${y + 4}" fill="${e.p.color}" font-family="DM Sans" font-size="11" font-weight="600">${e.n}<tspan fill="#77716c" font-weight="400"> /h</tspan></text>`;
    }
    svg.innerHTML = g;
  }

  const models = (s, n = 6) => {
    const top = s.usage.models.slice(0, n), mx = top[0].tokens;
    return top.map(m => {
      const p = s.providers[m.provider];
      return `<span class="m">${logo(p.logo, p.color, 11)}${m.model}</span><span class="b" style="width:${(m.tokens / mx) * 100}%;background:${p.color}"></span><span class="v">${fmt.tokens(m.tokens)}</span>`;
    }).join('');
  };

  const stats = s => {
    const u = s.usage;
    return `<div><b class="num">${fmt.num(u.requests)}</b><span>requests</span></div>
      <div><b class="num">${fmt.tokens(u.tokens)}</b><span>tokens</span></div>
      <div><b class="num">${(u.successRate * 100).toFixed(1)}%</b><span>success · ${u.failed} failed</span></div>`;
  };

  // Footer stamp, so a screenshot carries its own time and scope.
  const stamp = s => `${fmt.clock(s.fetchedAt)} IST · ${s.creds.length} credentials · ${s.pools.length} providers`;

  window.ui = { shadeOf, seg, amount, reset, spark, models, stats, stamp };
})();
