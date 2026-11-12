import {comparable} from './history.mjs';

const HOUR = 3600000;
const WINDOW_MS = {'5h':5*HOUR,'week':7*24*HOUR};

// bridgeable requires matching identities and a known allowance period across missing polls.
function bridgeable(a, b) {
  if (!a || !b || a.row.account !== b.row.account || !a.row.plan || a.row.plan !== b.row.plan || a.row.epoch !== b.row.epoch || !a.window.id || a.window.id !== b.window.id) return false;
  const first = Date.parse(a.window.reset_at), last = Date.parse(b.window.reset_at);
  const tolerance = a.window.reset_estimated || b.window.reset_estimated ? 120000 : 1000;
  return Number.isFinite(first) && Number.isFinite(last) && Math.abs(first-last)<=tolerance && b.time<Math.min(first,last);
}

// readPoint validates one saved row for the requested measured window.
function readPoint(row, kind) {
  const window = row.windows?.find(item => item.kind === kind);
  const time = Date.parse(row.at);
  if (row.failure || !window || window.inferred || !Number.isFinite(time) || !Number.isFinite(window.left) || window.left < 0 || window.left > 1) return null;
  const reset = window.reset_at ? Date.parse(window.reset_at) : NaN;
  if (Number.isFinite(reset) && time >= reset) return null;
  return {row,window,time};
}

// fresh checks whether a saved state is recent enough for a complete total.
function fresh(point, time, everySeconds) {
  return !!point && (!point.window.reset_at || Date.parse(point.window.reset_at)>time) && time >= point.time && time-point.time <= Math.max(75000, Math.max(everySeconds, point.row.every_s || everySeconds)*1500);
}

// samePeriod permits an observed reset to join while keeping other changes separate.
function samePeriod(previous, current) {
  if (!previous || !current || previous.row.account !== current.row.account || previous.row.epoch !== current.row.epoch || previous.row.plan !== current.row.plan || !current.row.plan || previous.window.id !== current.window.id || !current.window.id) return false;
  if (previous === current) return true;
  const gap = current.time-previous.time;
  const tolerance = Math.max(75000, Math.max(previous.row.every_s || 0,current.row.every_s || 0)*1500);
  if (gap <= 0 || gap > tolerance) return false;
  if (comparable(previous,current)) return true;
  const oldReset = Date.parse(previous.window.reset_at);
  const newReset = Date.parse(current.window.reset_at);
  return Number.isFinite(oldReset) && Number.isFinite(newReset) && oldReset <= current.time && newReset > current.time;
}

// trailingPoints returns recent comparable measurements from the latest period.
function trailingPoints(rows, account, kind, now) {
  const accountRows = [...new Map(rows.filter(row => row.account === account).sort((a,b)=>Date.parse(a.at)-Date.parse(b.at)).map(row=>[Date.parse(row.at),row])).values()];
  let run = [];
  for (const row of accountRows) {
    const point = readPoint(row,kind);
    if (!point || point.time > now) { run = []; continue; }
    if (run.length && !comparable(run.at(-1),point)) run = [];
    run.push(point);
  }
  return run.filter(point=>point.time>=now-HOUR);
}

// projectedValue drains one account and applies its first reported future reset.
function projectedValue(state, rate, time) {
  const reset = state.reset;
  const start = reset && reset > state.now ? reset : Infinity;
  if (time < start) return Math.max(0,state.left-rate*(time-state.now)/HOUR);
  return Math.max(0,100-rate*(time-start)/HOUR);
}

// buildPool sums fresh measured balances and projects only complete, comparable account history.
export function buildPool(rows, accounts, kind, now, everySeconds) {
  const unique = [...new Map(accounts.map(account=>[account.key,account])).keys()];
  const capacity = unique.length*100;
  if (!unique.length) return {capacity,segments:[],bridges:[],latest:null,complete:false,refills:[],projection:[],rate:null};
  const segments = [];
  const bridges = [];
  let lastCompleteSnapshot = null;
  const states = new Map();
  const selected = new Set(unique);
  let previousSnapshot = null;
  let latest = null;
  let wasComplete = false;
  const ordered = rows.filter(row=>selected.has(row.account) && Date.parse(row.at)<=now).sort((a,b)=>Date.parse(a.at)-Date.parse(b.at));

  for (let index=0; index<ordered.length;) {
    const time = Date.parse(ordered[index].at);
    if (!Number.isFinite(time)) { index++; continue; }
    let end = index+1;
    while (end<ordered.length && Date.parse(ordered[end].at)===time) end++;
    let invalid = false;
    for (const row of ordered.slice(index,end)) {
      if (!selected.has(row.account)) continue;
      const point = readPoint(row,kind);
      if (!point) {
        states.delete(row.account);
        invalid = true;
      } else states.set(row.account,point);
    }
    if (invalid) {
      previousSnapshot = null;
      wasComplete = false;
    }
    for (const key of unique) if (!fresh(states.get(key),time,everySeconds)) states.delete(key);
    if (unique.some(key=>!states.has(key))) {
      wasComplete = false;
      index = end;
      continue;
    }
    const snapshot = new Map(unique.map(key=>[key,states.get(key)]));
    const continuous = wasComplete && previousSnapshot && unique.every(key=>samePeriod(previousSnapshot.get(key),snapshot.get(key)));
    const value = unique.reduce((sum,key)=>sum+snapshot.get(key).window.left*100,0);
    const item = {time,value};
    if (!continuous) {
      if (latest && lastCompleteSnapshot && unique.every(key=>bridgeable(lastCompleteSnapshot.get(key),snapshot.get(key)))) bridges.push([latest,item]);
      segments.push([item]);
    }
    else segments.at(-1).push(item);
    latest = item;
    previousSnapshot = snapshot;
    lastCompleteSnapshot = snapshot;
    wasComplete = true;
    index = end;
  }

  const complete = unique.length > 0 && unique.every(key=>fresh(states.get(key),now,everySeconds) && (!states.get(key).window.reset_at || Date.parse(states.get(key).window.reset_at)>now));
  const projection = [], refills = [];
  let rate = null;
  if (complete && (kind === '5h' || kind === 'week')) {
    const histories = unique.map(key=>trailingPoints(ordered,key,kind,now));
    const eligible = histories.every(points=>points.length>=3 && points.at(-1).time-points[0].time>=15*60000 && now-points[0].time<=HOUR && fresh(points.at(-1),now,everySeconds));
    if (eligible) {
      const rates = histories.map(points=>Math.max(0,(points[0].window.left-points.at(-1).window.left)*100*HOUR/(points.at(-1).time-points[0].time)));
      rate = rates.reduce((sum,value)=>sum+value,0);
      const current = unique.map((key,index)=>{
        const point = states.get(key);
        const reset = point.window.reset_at ? Date.parse(point.window.reset_at) : NaN;
        return {now,left:point.window.left*100,rate:rates[index],reset:Number.isFinite(reset) && reset>now ? reset : null};
      });
      const end = now+Math.min(WINDOW_MS[kind],24*HOUR);
      const resetTimes = [...new Set(current.map(state=>state.reset).filter(time=>time && time<=end))].sort((a,b)=>a-b);
      const cadence = Math.max(60000,Math.min(15*60000,everySeconds*1000));
      const times = new Set([now,end]);
      for (let time=now+cadence; time<end; time+=cadence) times.add(time);
      for (const time of resetTimes) times.add(time);
      const sortedTimes = [...times].sort((a,b)=>a-b);
      const aggregate = time=>current.reduce((sum,state)=>sum+projectedValue(state,state.rate,time),0);
      projection.push({time:now,value:aggregate(now)});
      for (const time of sortedTimes.slice(1)) {
        if (resetTimes.includes(time)) {
          const before = current.reduce((sum,state)=>sum+(state.reset===time ? Math.max(0,state.left-state.rate*(time-now)/HOUR) : projectedValue(state,state.rate,time)),0);
          projection.push({time,value:before});
          const after = current.reduce((sum,state)=>sum+(state.reset===time ? 100 : projectedValue(state,state.rate,time)),0);
          projection.push({time,value:after});
          refills.push({time,value:after});
        } else projection.push({time,value:aggregate(time)});
      }
    }
  }
  return {capacity,segments,bridges,latest,complete,refills,projection,rate};
}
