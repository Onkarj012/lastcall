import {buildPool} from './pool.mjs';
const HOUR = 3600000;
const IST = new Intl.DateTimeFormat('en-IN', {timeZone:'Asia/Kolkata', day:'2-digit', month:'short', hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false});

// rangeStart uses midnight IST for Today and rolling windows for longer ranges.
export function rangeStart(now, range) {
  const offset = 5.5*HOUR, day = 24*HOUR;
  return range === 'today' ? Math.floor((now+offset)/day)*day-offset : now-({'24h':24,'7d':168,'30d':720}[range]*HOUR);
}

// stamp formats a recorded timestamp in the user's timezone.
function stamp(value) { return value ? `${IST.format(new Date(value))} IST` : 'Unknown'; }

// percent renders a measured remaining fraction without hiding small changes.
function percent(value) { return `${(value * 100).toFixed(1).replace(/\.0$/, '')}%`; }

// comparable permits drain calculations only within the same observed allowance period.
export function comparable(a, b) {
  if (!adjacent(a, b)) return false;
  if (!a.window.reset_at || !b.window.reset_at) return false;
  const ra = Date.parse(a.window.reset_at), rb = Date.parse(b.window.reset_at);
  const tolerance = a.window.reset_estimated || b.window.reset_estimated ? 120000 : 1000;
  return Number.isFinite(ra) && Number.isFinite(rb) && Math.abs(ra-rb) <= tolerance && b.time < Math.min(ra, rb);
}

// adjacent identifies nearby measurements from the same recording session and plan.
function adjacent(a, b) {
  if (!a || !b || a.row.account !== b.row.account || a.row.epoch !== b.row.epoch || a.row.plan !== b.row.plan || !a.row.plan || a.window.id !== b.window.id || !a.window.id) return false;
  const gap = b.time-a.time;
  return gap > 0 && gap <= Math.max(75000, Math.max(a.row.every_s, b.row.every_s) * 1500);
}

// buildSeries separates observations at failures, inferred values, pauses, and resets.
export function buildSeries(rows, kind) {
  const segments = [], observations = [];
  let previous = null, drop = 0, increase = 0, compared = 0;
  for (const row of rows) {
    const window = row.windows?.find(w => w.kind === kind);
    const time = Date.parse(row.at);
    const measured = !row.failure && window && !window.inferred && Number.isFinite(window.left) && window.left >= 0 && window.left <= 1 && Number.isFinite(time);
    const point = {row, window, time, measured, delta:null};
    if (measured) {
      if (comparable(previous, point)) {
        point.delta = (window.left - previous.window.left) * 100;
        drop += Math.max(0, -point.delta);
        increase += Math.max(0, point.delta);
        compared++;
        segments.at(-1).push(point);
      } else {
        if (adjacent(previous, point) && window.left > previous.window.left) {
          point.delta = (window.left - previous.window.left) * 100;
          increase += point.delta;
        }
        segments.push([point]);
      }
      previous = point;
    } else previous = null;
    observations.push(point);
  }
  return {segments, observations, drop, increase, compared};
}

// buildAccounts keeps each unique account's measurements and drain independent.
export function buildAccounts(rows, accounts, kind) {
  const grouped = new Map(accounts.map(account => [account.key, []]));
  for (const row of rows) grouped.get(row.account)?.push(row);
  return accounts.map((account, index) => ({...account, color:account.provider === 'codex' ? ['#85aaf2','#b5cafd','#6397c4'][index % 3] : ['#cc785c','#efb19a','#d39965'][index % 3], series:buildSeries(grouped.get(account.key).sort((a,b) => Date.parse(a.at)-Date.parse(b.at)),kind)}));
}

// resetMarkers keeps reported reset times inside the chart without multiplying estimated timestamps.
export function resetMarkers(series, start, end) {
  const markers = [];
  for (const point of series.observations) {
    if (!point.measured || !point.window.reset_at) continue;
    const time = Date.parse(point.window.reset_at), estimated = !!point.window.reset_estimated;
    if (!Number.isFinite(time) || time < start || time > end) continue;
    if (markers.some(m => Math.abs(m.time-time) <= (m.estimated || estimated ? 120000 : 1000))) continue;
    markers.push({time, estimated});
  }
  return markers;
}

// escapeHTML keeps provider labels and saved metadata out of executable markup.
function escapeHTML(value) { return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

// api reads saved observations without making another provider request.
async function api(path) {
  const response = await fetch(path);
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'History request failed.');
  return result;
}

const PROVIDERS = {claude:{name:'Claude', color:'#ed987b', logo:'claude'}, codex:{name:'Codex', color:'#8cb4fa', logo:'openai'}};
const IST_OFFSET = 5.5*HOUR, DAY = 24*HOUR;
const istFormat = options => new Intl.DateTimeFormat('en-IN', {timeZone:'Asia/Kolkata', hourCycle:'h23', ...options});

// axisTicks picks IST-aligned time labels that fit the plot width.
function axisTicks(start, end, plotWidth) {
  const fit = Math.max(2, Math.floor(plotWidth/64));
  const step = [HOUR, 3*HOUR, 6*HOUR, 12*HOUR, DAY, 2*DAY, 3*DAY, 5*DAY, 10*DAY].find(s => (end-start)/s <= fit) || 10*DAY;
  const ticks = [];
  for (let time = Math.ceil((start+IST_OFFSET)/step)*step-IST_OFFSET; time <= end; time += step) {
    const midnight = (time+IST_OFFSET) % DAY === 0;
    const label = step >= DAY || (midnight && time !== start) ? istFormat({weekday:'short', day:'numeric'}).format(time) : istFormat({hour:'2-digit', minute:'2-digit'}).format(time);
    ticks.push({time, label});
  }
  return ticks;
}

// axisMarkup draws the vertical time grid and its labels along a chart's bottom edge.
function axisMarkup(ticks, x, left, right, top, bottom, labelY) {
  return ticks.map(({time, label}) => `<line x1="${x(time)}" x2="${x(time)}" y1="${top}" y2="${bottom}" stroke="#181818"/><text x="${x(time)}" y="${labelY}" text-anchor="${x(time)-left < 24 ? 'start' : right-x(time) < 24 ? 'end' : 'middle'}">${label}</text>`).join('');
}

// ago names elapsed time in its largest sensible unit.
function ago(ms) {
  const minutes = Math.max(1, Math.round(ms/60000));
  return minutes < 60 ? `${minutes} min` : minutes < 2880 ? `${Math.round(minutes/60)} h` : `${Math.round(minutes/1440)} days`;
}

// startPage wires the history page without running browser code in chart tests.
function startPage() {
  const $ = id => document.getElementById(id);
  let range = 'today', kind = '5h', account = 'all', data, dataRange, loading = false, pending = false;
  let chartPoints = [], activePoint = -1, geometry, poolsMarkup = '', refocus = '';
  const windowName = () => kind === '5h' ? '5-hour' : 'Weekly';

  // drawChart renders one account's observed points and leaves unobserved time blank.
  function drawChart(groups) {
    const svg = $('chart'), now = Date.parse(data.now);
    const width = Math.max(260, svg.parentElement.clientWidth), height = width < 600 ? 260 : 340;
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    const start = rangeStart(now,range), left = 48, right = width-12, top = 20, bottom = height-34;
    const x = t => left+(t-start)/(now-start)*(right-left);
    const y = v => bottom-v*(bottom-top);
    const grid = [0,.5,1].map(v => `<line x1="${left}" x2="${right}" y1="${y(v)}" y2="${y(v)}" stroke="#222"/><text x="40" y="${y(v)+4}" text-anchor="end">${v*100}%</text>`).join('');
    const ticks = axisMarkup(axisTicks(start, now, right-left), x, left, right, top, bottom, height-10);
    const paths = groups.map(group => group.series.segments.map(segment => {
      const points = segment.map(p => `${x(p.time).toFixed(2)},${y(p.window.left).toFixed(2)}`).join(' ');
      const area = groups.length === 1 && segment.length > 1 ? `<polygon points="${x(segment[0].time)},${bottom} ${points} ${x(segment.at(-1).time)},${bottom}" fill="url(#area)"/>` : '';
      return `${area}<polyline points="${points}" fill="none" stroke="${group.color}" stroke-width="2.5" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>`;
    }).join('')).join('');
    chartPoints = groups.flatMap(group => group.series.observations.filter(p => p.measured).map(p => ({...p, group}))).sort((a,b) => a.time-b.time);
    activePoint = -1;
    geometry = {x,y,start,now,left,right,top,bottom};
    $('chart-tooltip').hidden = true;
    const points = groups.map(group => group.series.segments.flatMap(segment => segment.length > 1 ? [segment[0],segment.at(-1)] : segment).map(p => `<circle cx="${x(p.time)}" cy="${y(p.window.left)}" r="2.5" fill="${group.color}"/>`).join('')).join('');
    const markers = groups.flatMap(group => resetMarkers(group.series, start, now));
    const resets = markers.map(m => `<line class="reset-marker" x1="${x(m.time)}" x2="${x(m.time)}" y1="${top}" y2="${bottom}" stroke="#9a938d" stroke-opacity=".5" stroke-dasharray="3 5"><title>${m.estimated ? 'Estimated' : 'Reported'} reset: ${escapeHTML(stamp(m.time))}. Not a confirmed refill.</title></line>`).join('');
    $('reset-legend').hidden = !markers.length;
    svg.innerHTML = `<desc id="chart-description">Recorded remaining ${kind === '5h' ? '5-hour' : 'weekly'} allowance. Gaps show missing or incomparable readings. Dashed markers show reported reset times, not confirmed refills. Use left and right arrow keys to inspect exact values, or open Saved readings below.</desc><defs><linearGradient id="area" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="var(--series)" stop-opacity=".22"/><stop offset="100%" stop-color="var(--series)" stop-opacity="0"/></linearGradient></defs><g>${grid}${ticks}${resets}${paths}${points}</g><g id="chart-cursor" visibility="hidden"><line id="cursor-line" y1="${top}" y2="${bottom}" stroke="#9a938d" stroke-dasharray="3 4"/><circle id="cursor-dot" r="5" fill="var(--series)" stroke="#0f0f0f" stroke-width="2"/></g>`;
  }

  // poolNote says why a total is missing, stale or projected, and stays empty for a healthy total.
  function poolNote({pool, forecast}, now) {
    if (!pool.latest) return ['warn', 'No complete reading in this range. A total needs a fresh reading from every account.'];
    if (!pool.complete) return ['warn', `Some accounts have no fresh reading. This is the last complete total, from ${ago(now-pool.latest.time)} ago.`];
    if (forecast) return ['', `Forecast: ${Number(pool.rate.toFixed(1))}% per hour from the last hour of readings, with a full refill at each reported reset. Real limits can differ.`];
    if ($('projection').checked) return ['', 'Forecast needs 15 minutes of steady readings from every account.'];
    return null;
  }

  // accountRows lists each account's latest balance so the pool can be traced to its parts.
  function accountRows(provider, groups, now) {
    return groups.filter(g => g.provider === provider).map(g => {
      const latest = g.series.observations.filter(p => p.measured).at(-1);
      const lastPoll = g.series.observations.at(-1);
      const stale = !lastPoll?.measured || !latest || now-latest.time > Math.max(300, data.quota_every_s*2)*1000;
      const key = escapeHTML(g.key), plan = [latest?.row.plan, stale ? 'stale' : ''].filter(Boolean).join(' · ');
      return `<li><button type="button" data-account="${key}" data-focus="account-${key}" title="Open ${escapeHTML(g.label)} chart"><span class="acct-name">${escapeHTML(g.label)}${plan ? `<small>${escapeHTML(plan)}</small>` : ''}</span><span class="meter" aria-hidden="true"><i style="width:${latest ? latest.window.left*100 : 0}%"></i></span><b${stale ? ' class="stale"' : ''}>${latest ? percent(latest.window.left) : '—'}</b><span class="chev" aria-hidden="true">›</span></button></li>`;
    }).join('');
  }

  // poolShell renders a provider card without its chart, which needs the measured width.
  function poolShell(model, groups, now) {
    const {provider, accounts, pool, forecast, name, color, logo} = model, latest = pool.latest;
    const state = !latest ? 'empty' : pool.complete ? 'live' : 'stale';
    const icon = window.LOGOS?.[logo], note = poolNote(model, now);
    const keys = `<span class="key recorded"><i></i>Recorded</span>${pool.bridges.length ? '<span class="key bridge"><i></i>Gap estimate</span>' : ''}${forecast ? '<span class="key projected"><i></i>Projected</span>' : ''}`;
    return `<article class="pool-card" data-provider="${provider}" data-state="${state}" style="--pool-color:${color}" aria-labelledby="pool-title-${provider}"><header><h2 id="pool-title-${provider}"><span class="provider-mark" aria-hidden="true">${icon ? `<svg viewBox="0 0 24 24" fill="currentColor">${icon}</svg>` : ''}</span>${name}</h2><span class="count">${accounts.length} ${accounts.length === 1 ? 'account' : 'accounts'}</span>${state === 'stale' ? '<span class="chip">Stale</span>' : ''}</header><p class="pool-balance"><b>${latest ? `${Number(latest.value.toFixed(1))}%` : '—'}</b><span>of ${pool.capacity}%</span></p><p class="pool-caption">${windowName()} remaining · ${latest ? escapeHTML(stamp(latest.time)) : 'no complete reading'}</p><div class="pool-graph"></div><div class="pool-legend">${keys}</div>${note ? `<p class="pool-note" data-tone="${note[0]}">${note[1]}</p>` : ''}<ul class="pool-accounts" aria-label="${name} accounts">${accountRows(provider, groups, now)}</ul></article>`;
  }

  // poolChart draws recorded readings, gap estimates, the forecast and the Now marker at the card's real width.
  function poolChart(model, now, width) {
    const {provider, pool, forecast, name, color} = model, latest = pool.latest;
    const start = rangeStart(now,range), end = forecast ? pool.projection.at(-1).time : now;
    const height = Math.round(Math.max(220, Math.min(320, width*.5))), left = 46, right = width-14, top = 30, bottom = height-30;
    const x = t => left+(t-start)/(end-start)*(right-left), y = v => bottom-v/pool.capacity*(bottom-top);
    const points = segment => segment.map(p => `${x(p.time).toFixed(2)},${y(p.value).toFixed(2)}`).join(' ');
    const grid = [0,.5,1].map(v => `<line x1="${left}" x2="${right}" y1="${y(pool.capacity*v)}" y2="${y(pool.capacity*v)}" stroke="#222"/><text x="${left-10}" y="${y(pool.capacity*v)+4}" text-anchor="end">${Math.round(pool.capacity*v)}%</text>`).join('');
    const ticks = axisMarkup(axisTicks(start, end, right-left), x, left, right, top, bottom, bottom+20);
    const recorded = pool.segments.map(segment => segment.length > 1
      ? `<polygon points="${x(segment[0].time)},${bottom} ${points(segment)} ${x(segment.at(-1).time)},${bottom}" fill="url(#fill-${provider})"/><polyline class="pool-recorded" points="${points(segment)}" fill="none" stroke="${color}" stroke-width="2.5" stroke-linejoin="round"/>`
      : `<circle class="pool-recorded" cx="${x(segment[0].time)}" cy="${y(segment[0].value)}" r="2.5" fill="${color}"/>`).join('');
    const bridges = pool.bridges.map(segment => `<polyline class="pool-bridge" points="${points(segment)}" fill="none" stroke="${color}" stroke-opacity=".7" stroke-width="2" stroke-dasharray="1.5 5" stroke-linecap="round"><title>Estimated average change between recorded readings. Missing polls are not measured use.</title></polyline>`).join('');
    const first = pool.segments[0]?.[0];
    const blank = first && x(first.time)-left > 150 ? `<text class="muted-note" x="${(left+x(first.time))/2}" y="${(top+bottom)/2}" text-anchor="middle">No readings before ${istFormat(range === 'today' ? {hour:'2-digit', minute:'2-digit'} : {day:'numeric', month:'short', hour:'2-digit', minute:'2-digit'}).format(first.time)}</text>` : '';
    const flat = pool.segments.flat(), jumps = [];
    for (let i = 1; i < flat.length; i++) {
      const change = flat[i].value-flat[i-1].value;
      if (change > 1 && flat[i].time-flat[i-1].time <= data.quota_every_s*1500 && (!jumps.length || x(flat[i].time)-x(jumps.at(-1).time) > 56)) jumps.push({...flat[i], change});
    }
    const jumpLabels = jumps.slice(-4).map(p => `<text class="note" x="${x(p.time)}" y="${Math.max(top-2, y(p.value)-10)}" text-anchor="middle">+${Math.round(p.change)}%</text>`).join('');
    const refills = forecast ? pool.refills.map(r => {
      const index = pool.projection.findIndex(p => p.time === r.time && p.value === r.value), before = pool.projection[index-1], flip = x(r.time) > right-70;
      return `<text class="note projected-note" x="${x(r.time)+(flip ? -6 : 6)}" y="${Math.max(top+12, y(r.value)-10)}" text-anchor="${flip ? 'end' : 'start'}" fill="${color}">+${Math.round(before ? r.value-before.value : 0)}% reset</text>`;
    }).join('') : '';
    const projected = forecast ? `<rect class="forecast-zone" x="${x(now)}" y="${top}" width="${right-x(now)}" height="${bottom-top}"/><text class="muted-note" x="${x(now)+20}" y="${top-14}">Forecast</text><polyline class="pool-projected" points="${points(pool.projection)}" fill="none" stroke="${color}" stroke-width="2.5" stroke-dasharray="7 6" stroke-linejoin="round"/>${refills}` : '';
    const dot = latest ? `<circle cx="${x(latest.time)}" cy="${y(latest.value)}" r="5.5" fill="${pool.complete ? color : '#0f0f0f'}" stroke="${pool.complete ? '#0f0f0f' : color}" stroke-width="${pool.complete ? 3 : 2.5}"/>` : '';
    const label = latest ? `${Number(latest.value.toFixed(1))} percent of ${pool.capacity} percent` : 'no complete reading';
    const markup = `<svg viewBox="0 0 ${width} ${height}" role="img" tabindex="0" data-focus="chart-${provider}" aria-label="${name} pooled ${windowName().toLowerCase()} remaining allowance, ${label}. Use left and right arrow keys to inspect readings."><defs><linearGradient id="fill-${provider}" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="${color}" stop-opacity=".3"/><stop offset="100%" stop-color="${color}" stop-opacity="0"/></linearGradient></defs>${grid}${ticks}${blank}${recorded}${bridges}${projected}${jumpLabels}<line class="now-line" x1="${x(now)}" x2="${x(now)}" y1="${top-8}" y2="${bottom+6}"/><text class="now-label" x="${x(now)}" y="${top-14}" text-anchor="${right-x(now) < 18 ? 'end' : 'middle'}">Now</text>${dot}<g class="pool-cursor" visibility="hidden"><line y1="${top}" y2="${bottom}"/><circle r="5" fill="${color}" stroke="#0f0f0f" stroke-width="2"/></g></svg>${latest ? '' : '<p class="pool-empty">No complete pool readings in this range</p>'}<output class="chart-tooltip" hidden></output>`;
    return {markup, x, y, width};
  }

  // inspectPool lets a pointer or the arrow keys read one recorded pool total.
  function inspectPool(graph, {pool, name, color}, {x, y, width}) {
    const svg = graph.querySelector('svg'), tip = graph.querySelector('.chart-tooltip'), cursor = svg.querySelector('.pool-cursor');
    const points = pool.segments.flatMap(segment => segment.map((p, i) => ({...p, change: i ? p.value-segment[i-1].value : null})));
    if (!points.length) return;
    let active = -1;
    const show = index => {
      active = Math.max(0, Math.min(index, points.length-1));
      const p = points[active], px = x(p.time);
      cursor.setAttribute('visibility', 'visible');
      cursor.querySelector('line').setAttribute('x1', px); cursor.querySelector('line').setAttribute('x2', px);
      cursor.querySelector('circle').setAttribute('cx', px); cursor.querySelector('circle').setAttribute('cy', y(p.value));
      const change = p.change === null ? 'First reading after a gap' : `${p.change > 0 ? '+' : ''}${Number(p.change.toFixed(1))}% since previous reading`;
      tip.innerHTML = `<span>${name}</span><b style="color:${color}">${Number(p.value.toFixed(1))}% <small>of ${pool.capacity}%</small></b><span>${escapeHTML(stamp(p.time))}</span><span>${escapeHTML(change)}</span>`;
      tip.style.left = `${Math.max(0, Math.min(px-110, width-232))}px`;
      tip.hidden = false;
    };
    const hide = () => { cursor.setAttribute('visibility', 'hidden'); tip.hidden = true; active = -1; };
    svg.addEventListener('pointermove', event => {
      const box = svg.getBoundingClientRect(), px = (event.clientX-box.left)*width/box.width;
      let index = 0;
      for (let i = 1; i < points.length; i++) if (Math.abs(x(points[i].time)-px) < Math.abs(x(points[index].time)-px)) index = i;
      if (index !== active) show(index);
    });
    svg.addEventListener('pointerleave', hide);
    svg.addEventListener('blur', hide);
    svg.addEventListener('keydown', event => {
      if (event.key === 'Escape') { hide(); return; }
      if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
      event.preventDefault();
      const current = active < 0 ? points.length-1 : active;
      show(event.key === 'Home' ? 0 : event.key === 'End' ? points.length-1 : current+(event.key === 'ArrowLeft' ? -1 : 1));
    });
  }

  // drawPools renders one card per provider and keeps keyboard focus where it was.
  function drawPools(choices, groups) {
    const now = Date.parse(data.now), focused = $('pools').contains(document.activeElement) ? document.activeElement.dataset.focus : '';
    const models = ['claude','codex'].filter(provider => choices.some(a => a.provider === provider)).map(provider => {
      const accounts = choices.filter(a => a.provider === provider);
      const pool = buildPool(data.records.filter(r => r.provider === provider), accounts, kind, now, data.quota_every_s);
      return {provider, accounts, pool, forecast:$('projection').checked && pool.projection.length > 1, ...PROVIDERS[provider]};
    });
    const markup = models.map(model => poolShell(model, groups, now)).join('');
    if (markup !== poolsMarkup) $('pools').innerHTML = poolsMarkup = markup;
    for (const graph of $('pools').querySelectorAll('.pool-graph')) {
      const model = models.find(m => m.provider === graph.closest('.pool-card').dataset.provider);
      const chart = poolChart(model, now, Math.max(240, graph.clientWidth));
      graph.innerHTML = chart.markup;
      inspectPool(graph, model, chart);
    }
    if (focused) [...$('pools').querySelectorAll('[data-focus]')].find(el => el.dataset.focus === focused)?.focus();
  }

  // setView shows the provider pools, one account's chart, or a status message.
  function setView(view) {
    $('pools').hidden = view !== 'pools';
    $('detail').hidden = view === 'pools';
    $('detail').dataset.view = view;
    document.querySelector('.forecast').hidden = view === 'detail';
  }

  // inspectPoint reveals one measured value without connecting across a gap.
  function inspectPoint(index) {
    if (!chartPoints.length || !geometry) return;
    activePoint = Math.max(0,Math.min(index,chartPoints.length-1));
    const point = chartPoints[activePoint], x = geometry.x(point.time), y = geometry.y(point.window.left);
    $('chart-cursor').setAttribute('visibility','visible');
    $('cursor-line').setAttribute('x1',x); $('cursor-line').setAttribute('x2',x);
    $('cursor-dot').setAttribute('cx',x); $('cursor-dot').setAttribute('cy',y);
    const tooltip = $('chart-tooltip');
    const change = point.delta === null ? 'No comparable previous reading' : `${point.delta > 0 ? '+' : ''}${point.delta.toFixed(1)} pp since previous reading`;
    const reset = point.window.reset_at ? `${point.window.reset_estimated ? 'Estimated' : 'Reported'} reset ${stamp(point.window.reset_at)}` : 'Reset time not reported';
    $('cursor-dot').setAttribute('fill',point.group.color);
    tooltip.innerHTML = `<span>${point.group.provider === 'codex' ? 'Codex' : 'Claude'} · ${escapeHTML(point.group.label)}</span><b style="color:${point.group.color}">${percent(point.window.left)} left</b><span>${escapeHTML(stamp(point.row.at))}</span><span>${escapeHTML(change)}</span><small>${escapeHTML(reset)}</small>`;
    tooltip.style.setProperty('--tooltip-x', `${x+20}px`);
    tooltip.hidden = false;
  }

  // hideInspection clears the chart cursor when pointer or keyboard inspection ends.
  function hideInspection() {
    if ($('chart-cursor')) $('chart-cursor').setAttribute('visibility','hidden');
    $('chart-tooltip').hidden = true;
    activePoint = -1;
  }

  // inspectPointer finds the nearest saved reading to a pointer location.
  function inspectPointer(event) {
    if (!chartPoints.length || !geometry) return;
    const box = $('chart').getBoundingClientRect();
    const px = (event.clientX-box.left)*$('chart').viewBox.baseVal.width/box.width;
    const py = (event.clientY-box.top)*$('chart').viewBox.baseVal.height/box.height;
    let index = 0, distance = Infinity;
    for (let i=0; i<chartPoints.length; i++) {
      const point = chartPoints[i], next = (geometry.x(point.time)-px)**2+(geometry.y(point.window.left)-py)**2;
      if (next < distance) { distance = next; index = i; }
    }
    if (index!==activePoint) inspectPoint(index);
  }

  // draw updates recording health and shows either the provider pools or one account.
  function draw() {
    if (!data || data.account !== account || dataRange !== range) return;
    $('error').textContent = data.read_error || data.status.error || '';
    account = data.account;
    const selected = data.accounts.find(a => a.key === account);
    const archived = data.accounts.filter(a => a.archived);
    $('archived').hidden = !archived.length;
    $('archived-accounts').innerHTML = archived.map(a => `<li><button type="button" data-account="${escapeHTML(a.key)}" data-focus="account-${escapeHTML(a.key)}"><span class="acct-name">${escapeHTML(PROVIDERS[a.provider]?.name || a.provider)} · ${escapeHTML(a.label)}</span><span class="chevron" aria-hidden="true">›</span></button></li>`).join('');
    const choices = data.selected || data.accounts.filter(a => account === 'all' || account === a.provider || account === a.key);
    const groups = buildAccounts(data.records, data.accounts, kind).filter(group => choices.some(choice => choice.key === group.key)).map(g => ({...g, color:PROVIDERS[g.provider]?.color || g.color}));
    const combined = ['all','claude','codex'].includes(account) && choices.length > 0;
    const series = combined ? {observations:groups.flatMap(g => g.series.observations).sort((a,b) => a.time-b.time), compared:0, drop:0, increase:0} : groups[0]?.series || buildSeries([],kind);
    setView(combined ? 'pools' : data.accounts.length ? 'detail' : 'status');
    $('detail').style.setProperty('--series', PROVIDERS[selected?.provider]?.color || PROVIDERS.claude.color);
    if (combined) drawPools(choices, groups);
    const measured = series.observations.filter(p => p.measured), latest = measured.at(-1);
    $('detail-name').textContent = selected ? `${PROVIDERS[selected.provider]?.name || 'Claude'} · ${selected.label}` : '';
    $('remaining').textContent = latest ? percent(latest.window.left) : '—';
    $('latest-time').textContent = latest ? stamp(latest.row.at) : 'No saved reading';
    $('account-detail').textContent = latest?.row.plan || '';
    $('chart-title').textContent = `${windowName()} remaining`;
    const excluded = series.observations.length-measured.length;
    $('coverage').textContent = `${excluded ? `${excluded} polls without a measured value. ` : ''}Gaps are unobserved. Lines stop at the last reading.`;
    const lastPoll = data.records.at(-1), failed = !!lastPoll?.failure && lastPoll.failure !== 'account_disabled';
    const stale = groups.some(g => { const p = g.series.observations.at(-1); return !p || p.row.failure || !p.measured || Date.parse(data.now)-p.time>Math.max(300,data.quota_every_s*2)*1000; });
    const error = data.read_error || data.status.error;
    $('recording-status').dataset.health = error ? 'error' : failed || stale ? 'warning' : !lastPoll ? 'waiting' : 'ok';
    $('health-text').textContent = error ? 'History needs attention' : failed ? 'Latest poll failed' : stale ? 'Waiting for a fresh reading' : !lastPoll ? 'Waiting for the first reading' : 'Recording automatically';
    $('chart').toggleAttribute('hidden', !measured.length);
    $('empty').hidden = !!measured.length;
    if (!measured.length) {
      const noAccounts = !data.accounts.length;
      $('empty').innerHTML = `<span class="empty-mark" aria-hidden="true">···</span><h3>${data.read_error ? 'History unavailable' : noAccounts ? 'No accounts yet' : 'Waiting for readings'}</h3><p>${data.read_error ? 'Check the history file error above.' : noAccounts ? '<a href="/">Add a Claude or Codex account</a> to start saving usage history.' : 'History saves automatically after each quota poll. Try a longer range for older readings.'}</p>`;
      chartPoints = []; hideInspection();
      $('reset-legend').hidden = true;
    } else if (!combined) drawChart(groups);
    $('drain').innerHTML = series.compared ? `${series.drop.toFixed(1)}<small>pp</small>` : '—';
    $('increases').innerHTML = series.compared || series.increase ? `+${series.increase.toFixed(1)}<small>pp</small>` : '—';
    const reset = latest?.window.reset_at;
    $('reset-time').textContent = !combined && reset ? `Reported reset ${latest.window.reset_estimated ? '~ ' : ''}${stamp(reset)}` : '';
    $('saved-status').textContent = measured.length ? `${measured.length} readings · ${choices.length} ${choices.length === 1 ? 'account' : 'accounts'}` : 'Readings save automatically';
    $('poll-note').textContent = `Saved every ${Math.round(data.quota_every_s/60)} min · 30-day retention`;
    $('reading-count').textContent = `${data.records.length} polls`;
    $('readings').innerHTML = series.observations.slice(-100).reverse().map(p => {
      const note = p.row.failure === 'account_disabled' ? 'Account disabled; recording gap' : p.row.failure ? 'Poll failed' : !p.window ? 'Window not reported' : p.window.inferred ? 'Inferred; excluded from chart' : 'Measured';
      const change = p.delta === null ? '—' : `${p.delta > 0 ? '+' : ''}${p.delta.toFixed(1)} pp`;
      const owner = choices.find(a => a.key === p.row.account);
      return `<tr><td>${escapeHTML(owner ? `${owner.provider === 'codex' ? 'Codex' : 'Claude'} · ${owner.label}` : 'Saved account')}</td><td>${escapeHTML(stamp(p.row.at))}</td><td>${p.measured ? percent(p.window.left) : '—'}</td><td>${change}</td><td>${escapeHTML(p.window?.reset_at ? `${p.window.reset_estimated ? '~ ' : ''}${stamp(p.window.reset_at)}` : 'Unknown')}</td><td>${note}</td></tr>`;
    }).join('') || '<tr><td colspan="6">No saved polls in this range.</td></tr>';
    if (refocus) { [...document.querySelectorAll('[data-focus], #back')].find(el => el.matches(refocus))?.focus(); refocus = ''; }
  }

  // showPending clears the previous selection so its values cannot appear under new filters.
  function showPending() {
    $('panel').setAttribute('aria-busy', 'true');
    $('pools').innerHTML = poolsMarkup = '';
    setView('status');
    $('chart').toggleAttribute('hidden', true);
    $('empty').hidden = false;
    $('empty').innerHTML = '<span class="empty-mark" aria-hidden="true">···</span><h3>Loading saved readings</h3><p>Reading this account and time range.</p>';
    for (const id of ['remaining','drain','increases']) $(id).textContent = '—';
    for (const id of ['latest-time','account-detail','detail-name','reset-time','saved-status','reading-count','coverage']) $(id).textContent = '';
    $('chart-title').textContent = `${windowName()} remaining`;
    $('readings').innerHTML = '<tr><td colspan="6">Loading saved readings…</td></tr>';
    $('reset-legend').hidden = true;
    $('recording-status').dataset.health = 'waiting';
    $('health-text').textContent = 'Loading saved history';
    chartPoints = []; hideInspection();
  }

  // load reads saved data without asking providers for another quota refresh.
  async function load() {
    if (!data || data.account !== account || dataRange !== range) showPending();
    if (loading) { pending = true; return; }
    loading = true;
    const requestedRange = range, requestedAccount = account;
    try {
      const result = await api(`/api/history?${new URLSearchParams({range:requestedRange, account:requestedAccount})}`);
      if (range === requestedRange && account === requestedAccount) {
        data = result; dataRange = requestedRange; account = result.account;
        $('panel').setAttribute('aria-busy', 'false');
        draw();
      }
      else pending = true;
    } catch (error) {
      if (range !== requestedRange || account !== requestedAccount) { pending = true; return; }
      $('panel').setAttribute('aria-busy', 'false');
      $('error').textContent = error.message;
      $('recording-status').dataset.health = 'error';
      $('health-text').textContent = 'History unavailable';
      if (!data || data.account !== account || dataRange !== range) {
        $('empty').innerHTML = '<h3>History unavailable</h3><p>Saved readings could not be loaded.</p><button class="btn retry" type="button" id="retry-history">Try again</button>';
        $('retry-history').addEventListener('click', load);
      }
    }
    finally { loading = false; if (pending) { pending = false; load(); } }
  }

  // openAccount switches between the provider pools and one account's chart, then restores focus.
  function openAccount(key, focus) {
    account = key; refocus = focus;
    load();
  }

  $('projection').addEventListener('change',draw);
  $('pools').addEventListener('click', event => {
    const button = event.target.closest('button[data-account]');
    if (button) openAccount(button.dataset.account, '#back');
  });
  $('back').addEventListener('click', () => openAccount('all', `[data-focus="account-${account}"]`));
  $('archived-accounts').addEventListener('click', event => {
    const button = event.target.closest('button[data-account]');
    if (button) openAccount(button.dataset.account, '#back');
  });
  $('windows').addEventListener('click', event => {
    const button = event.target.closest('button[data-window]');
    if (!button) return;
    kind = button.dataset.window;
    for (const b of $('windows').querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
    if (data?.account === account && dataRange === range) draw(); else load();
  });
  $('ranges').addEventListener('click', event => {
    const button = event.target.closest('button[data-range]');
    if (!button) return;
    range = button.dataset.range;
    for (const b of $('ranges').querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
    load();
  });
  $('chart').addEventListener('pointermove', inspectPointer);
  $('chart').addEventListener('pointerdown', inspectPointer);
  $('chart').addEventListener('pointerleave', hideInspection);
  $('chart').addEventListener('blur', hideInspection);
  $('chart').addEventListener('keydown', event => {
    if (event.key==='Escape') { hideInspection(); return; }
    if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key) || !chartPoints.length) return;
    event.preventDefault();
    const current = activePoint<0 ? chartPoints.length-1 : activePoint;
    inspectPoint(event.key==='Home' ? 0 : event.key==='End' ? chartPoints.length-1 : current+(event.key==='ArrowLeft' ? -1 : 1));
  });
  load();
  setInterval(load, 30000);
  window.addEventListener('resize', () => { if (data?.account === account && dataRange === range) draw(); });
}

if (typeof document !== 'undefined') startPage();
