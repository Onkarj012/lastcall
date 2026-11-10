const HOUR = 3600000;
const IST = new Intl.DateTimeFormat('en-IN', {timeZone:'Asia/Kolkata', day:'2-digit', month:'short', hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false});

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
  if (!a || !b || a.row.epoch !== b.row.epoch || a.row.plan !== b.row.plan || !a.row.plan || a.window.id !== b.window.id || !a.window.id) return false;
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

// escapeHTML keeps provider labels and saved metadata out of executable markup.
function escapeHTML(value) { return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

// api reads history or saves the explicit recording preference.
async function api(path, body) {
  const response = await fetch(path, body === undefined ? {} : {method:'POST', headers:{'Content-Type':'application/json','X-Lastcall':'1'}, body:JSON.stringify(body)});
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'History request failed.');
  return result;
}

// startPage wires the history page without running browser code in chart tests.
function startPage() {
  const $ = id => document.getElementById(id);
  let range = '24h', kind = '5h', account = '', data, loading = false, pending = false, saving = false;

  // drawChart renders observed points only and leaves unobserved time blank.
  function drawChart(series) {
    const svg = $('chart'), now = Date.parse(data.now), hours = {'24h':24,'7d':168,'30d':720}[range];
    const width = Math.max(280, svg.parentElement.clientWidth-32), height = width < 600 ? 250 : 320;
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    const start = now-hours*HOUR, left = 48, right = width-16, top = 20, bottom = height-42;
    const x = t => left+(t-start)/(now-start)*(right-left);
    const y = v => bottom-v*(bottom-top);
    const grid = [0,.25,.5,.75,1].map(v => `<line x1="${left}" x2="${right}" y1="${y(v)}" y2="${y(v)}" stroke="#24211e"/><text x="40" y="${y(v)+4}" text-anchor="end" fill="#9a938d" font-size="11">${v*100}%</text>`).join('');
    const tickCount = width < 600 ? 3 : 5;
    const ticks = Array.from({length:tickCount}, (_, i) => {
      const t = start+(now-start)*i/(tickCount-1);
      const label = new Intl.DateTimeFormat('en-IN', {timeZone:'Asia/Kolkata', ...(range === '24h' ? {hour:'2-digit',minute:'2-digit',hour12:false} : {day:'2-digit',month:'short'})}).format(t);
      return `<text x="${x(t)}" y="${height-12}" text-anchor="${i === 0 ? 'start' : i === tickCount-1 ? 'end' : 'middle'}" fill="#9a938d" font-size="11">${label}</text>`;
    }).join('');
    const paths = series.segments.map(segment => {
      const points = segment.map(p => `${x(p.time).toFixed(2)},${y(p.window.left).toFixed(2)}`).join(' ');
      const area = segment.length > 1 ? `<polygon points="${x(segment[0].time)},${bottom} ${points} ${x(segment.at(-1).time)},${bottom}" fill="url(#area)"/>` : '';
      return `${area}<polyline points="${points}" fill="none" stroke="var(--series)" stroke-width="2" vector-effect="non-scaling-stroke"/>`;
    }).join('');
    const points = series.observations.filter(p => p.measured).map(p => `<circle cx="${x(p.time)}" cy="${y(p.window.left)}" r="2.5" fill="var(--series)"><title>${escapeHTML(`${stamp(p.row.at)} · ${percent(p.window.left)} remaining`)}</title></circle>`).join('');
    svg.innerHTML = `<desc id="chart-description">Recorded remaining ${kind === '5h' ? '5-hour' : 'weekly'} allowance. Gaps show missing or incomparable readings. Exact values are in Saved readings below.</desc><defs><linearGradient id="area" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="var(--series)" stop-opacity=".2"/><stop offset="100%" stop-color="var(--series)" stop-opacity=".01"/></linearGradient></defs><g font-family="DM Mono, monospace">${grid}${ticks}${paths}${points}</g>`;
  }

  // draw updates account controls, recording health, and the selected history.
  function draw() {
    if (!saving) $('recording').checked = data.enabled;
    $('recording').disabled = saving;
    $('recording-label').textContent = data.enabled ? 'On' : 'Off';
    const saved = data.status.last_saved && !data.status.last_saved.startsWith('0001-') ? stamp(data.status.last_saved) : 'No readings saved yet';
    $('recording-status').textContent = `${data.enabled ? 'Recording on' : 'Recording off'} · Last saved: ${saved}${data.records.at(-1)?.failure ? ' · Latest poll failed' : ''}`;
    $('error').textContent = data.read_error || data.status.error || '';
    account = data.account;
    $('account').innerHTML = data.accounts.length ? data.accounts.map(a => `<option value="${escapeHTML(a.key)}">${a.provider === 'codex' ? 'Codex' : 'Claude'} · ${escapeHTML(a.label)}</option>`).join('') : '<option>No Claude or Codex accounts</option>';
    $('account').value = account;
    $('account').disabled = !data.accounts.length;
    const selected = data.accounts.find(a => a.key === account);
    document.querySelector('.history-panel').style.setProperty('--series', selected?.provider === 'codex' ? '#85aaf2' : '#cc785c');
    const series = buildSeries(data.records, kind);
    const measured = series.observations.filter(p => p.measured), latest = measured.at(-1);
    $('remaining').textContent = latest ? percent(latest.window.left) : '—';
    $('latest-time').textContent = latest ? stamp(latest.row.at) : 'No saved reading';
    $('account-detail').textContent = `${kind === '5h' ? '5-hour' : 'Weekly'} allowance${latest?.row.plan ? ` · ${latest.row.plan}` : ''}`;
    $('chart').toggleAttribute('hidden', !measured.length);
    $('empty').hidden = !!measured.length;
    if (!measured.length) {
      $('empty').innerHTML = `<h3>${data.read_error ? 'History unavailable' : 'No readings in this range'}</h3><p>${data.read_error ? 'Resolve the file error above to read saved history.' : data.enabled ? 'Readings appear after the next successful quota poll. Try a longer range for older readings.' : 'Turn on recording to start saving readings from the next quota poll.'}</p>`;
    } else drawChart(series);
    $('summary').innerHTML = series.compared || series.increase ? `<span>Observed drops <b>${series.drop.toFixed(1)} percentage points</b></span><span>Observed increases <b>${series.increase.toFixed(1)} points</b></span><span><b>${series.compared}</b> comparable intervals</span>` : '';
    $('reading-count').textContent = `${data.records.length} polls`;
    $('readings').innerHTML = series.observations.slice(-100).reverse().map(p => {
      const note = p.row.failure ? 'Poll failed' : !p.window ? 'Window not reported' : p.window.inferred ? 'Inferred; excluded from chart' : 'Measured';
      const change = p.delta === null ? '—' : `${p.delta > 0 ? '+' : ''}${p.delta.toFixed(1)} pp`;
      return `<tr><td>${escapeHTML(stamp(p.row.at))}</td><td>${p.measured ? percent(p.window.left) : '—'}</td><td>${change}</td><td>${escapeHTML(p.window?.reset_at ? `${p.window.reset_estimated ? '~ ' : ''}${stamp(p.window.reset_at)}` : 'Unknown')}</td><td>${note}</td></tr>`;
    }).join('') || '<tr><td colspan="5">No saved polls in this range.</td></tr>';
  }

  // load reads saved data without asking providers for another quota refresh.
  async function load() {
    if (loading) { pending = true; return; }
    loading = true;
    const requestedRange = range, requestedAccount = account;
    try {
      const result = await api(`/api/history?${new URLSearchParams({range:requestedRange, account:requestedAccount})}`);
      if (range === requestedRange && account === requestedAccount) { data = result; draw(); }
      else pending = true;
    } catch (error) { $('error').textContent = error.message; }
    finally { loading = false; if (pending) { pending = false; load(); } }
  }

  // changeRecording saves the preference before updating the displayed status.
  async function changeRecording() {
    if (saving) return;
    saving = true;
    const enabled = $('recording').checked;
    const restoreFocus = document.activeElement === $('recording');
    $('recording').disabled = true;
    try { await api('/api/history/settings', {enabled}); await load(); }
    catch (error) { $('recording').checked = !enabled; $('error').textContent = error.message; }
    finally {
      saving = false;
      $('recording').disabled = false;
      if (restoreFocus && document.activeElement === document.body) $('recording').focus({preventScroll:true});
    }
  }

  $('recording').addEventListener('change', changeRecording);
  $('account').addEventListener('change', () => { account = $('account').value; load(); });
  $('windows').addEventListener('click', event => {
    const button = event.target.closest('button[data-window]');
    if (!button) return;
    kind = button.dataset.window;
    for (const b of $('windows').querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
    if (data) draw();
  });
  $('ranges').addEventListener('click', event => {
    const button = event.target.closest('button[data-range]');
    if (!button) return;
    range = button.dataset.range;
    for (const b of $('ranges').querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === button));
    load();
  });
  load();
  setInterval(load, 30000);
  window.addEventListener('resize', () => { if (data) draw(); });
}

if (typeof document !== 'undefined') startPage();
