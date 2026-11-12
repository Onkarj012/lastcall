import {test} from 'node:test';
import assert from 'node:assert/strict';
import {buildPool} from './pool.mjs';

const origin = Date.parse('2026-11-10T00:00:00Z');

// row creates one account measurement at a fixed five-hour reset.
function row(account, minute, left, extra = {}) {
  return {account,at:new Date(origin+minute*60000).toISOString(),epoch:'enabled',every_s:300,plan:'Pro',windows:[{id:'five_hour',kind:'5h',left,reset_at:new Date(origin+5*3600000).toISOString()}],...extra};
}

const accounts = [{key:'a'}, {key:'b'}];

test('pool sums remaining balances once per selected account', () => {
  const result = buildPool([row('a',0,.8),row('a',0,.7),row('b',0,.9)],[...accounts,{key:'a'}],'5h',origin,300);
  assert.equal(result.capacity,200);
  assert.deepEqual(result.segments,[ [{time:origin,value:160}] ]);
  assert.deepEqual(result.latest,{time:origin,value:160});
});

test('pool breaks at failed, missing, inferred, expired, and stale readings', () => {
  const failed = row('a',5,.6,{failure:'provider_error'});
  const missing = row('a',5,.6,{windows:[]});
  const inferred = row('a',5,.6);
  inferred.windows[0].inferred = true;
  for (const invalid of [failed,missing,inferred]) {
    const result = buildPool([row('a',0,.8),row('b',0,.8),invalid,row('b',5,.7),row('a',10,.6),row('b',10,.6)],accounts,'5h',origin+10*60000,300);
    assert.equal(result.segments.length,2);
    assert.equal(result.segments[0].length,1);
    assert.equal(result.segments[1].length,1);
  }
  const expired = buildPool([row('a',0,.8),row('b',0,.8)],accounts,'5h',origin+8*60000,300);
  assert.deepEqual(expired.latest,{time:origin,value:160});
  assert.equal(expired.complete,false);
  const resetA = row('a',0,.8), resetB = row('b',0,.8);
  for (const point of [resetA,resetB]) point.windows[0].reset_at = new Date(origin+5*60000).toISOString();
  const reset = buildPool([resetA,resetB],accounts,'5h',origin+6*60000,300);
  assert.deepEqual(reset.latest,{time:origin,value:160});
  assert.equal(reset.complete,false);
});

test('pool keeps observed reset jumps and breaks plan changes', () => {
  const before = [row('a',0,.2),row('b',0,.4)];
  for (const point of before) point.windows[0].reset_at = new Date(origin+5*60000).toISOString();
  const after = [row('a',5,.9),row('b',5,.5)];
  for (const point of after) point.windows[0].reset_at = new Date(origin+10*3600000).toISOString();
  const result = buildPool([...before,...after],accounts,'5h',origin+5*60000,300);
  assert.equal(result.segments.length,1);
  assert.deepEqual(result.segments[0].map(p=>p.value),[60,140]);
  const changed = buildPool([...before,row('a',5,.8,{plan:'Max'}),row('b',5,.3)],accounts,'5h',origin+5*60000,300);
  assert.equal(changed.segments.length,2);
});

test('projection needs comparable history and stays bounded with reset markers', () => {
  const rows = [];
  for (const minute of [0,5,10,15,20]) rows.push(row('a',minute,.9-minute/100),row('b',minute,.8-minute/200));
  const result = buildPool(rows,accounts,'5h',origin+20*60000,300);
  assert.equal(result.complete,true);
  assert.ok(result.rate > 0);
  assert.ok(result.projection.length > 1);
  assert.ok(result.projection.every(p=>p.value>=0 && p.value<=200));
  assert.ok(result.projection.at(-1).time <= origin+20*60000+5*3600000);
  assert.ok(result.refills.some(p=>p.time===origin+5*3600000));
  const short = buildPool([row('a',10,.8),row('b',10,.8)],accounts,'5h',origin+10*60000,300);
  assert.equal(short.projection.length,0);
  assert.equal(short.rate,null);
  const stale = buildPool(rows,accounts,'5h',origin+28*60000,300);
  assert.equal(stale.complete,false);
  assert.equal(stale.projection.length,0);
});

test('asynchronous polls keep the unchanged account connected without carrying it past reset', () => {
  const rows = [row('a',0,.8),row('b',1,.9),row('a',5,.7),row('b',6,.8)];
  const result=buildPool(rows,accounts,'5h',origin+6*60000,300);
  assert.equal(result.segments.length,1);
  assert.equal(result.segments[0].length,3);
  rows[0].windows[0].reset_at = new Date(origin+30*1000).toISOString();
  const expired=buildPool(rows.slice(0,2),accounts,'5h',origin+60000,300);
  assert.equal(expired.latest,null);
});

test('weekly projections use the saved week kind', () => {
  const rows=[];
  for(const minute of [0,5,10,15,20]) for(const account of ['a','b']) {
    const r=row(account,minute,.9-minute*.005);r.windows[0].kind='week';rows.push(r);
  }
  assert.ok(buildPool(rows,accounts,'week',origin+20*60000,300).projection.length>1);
});

test('projection uses the recent hour even after a longer uninterrupted session', () => {
  const rows=[];
  for(let minute=0;minute<=90;minute+=5) for(const account of ['a','b']) rows.push(row(account,minute,.95-minute*.004));
  assert.ok(buildPool(rows,accounts,'5h',origin+90*60000,300).projection.length>1);
});

test('gaps bridge known pool endpoints only within matching allowance periods', () => {
  const before=[row('a',0,.8),row('b',0,.9)];
  const after=[row('a',30,.6),row('b',30,.7)];
  const rows=[...before,row('a',5,0,{failure:'fetch_failed'}),...after];
  const result=buildPool(rows,accounts,'5h',origin+30*60000,300);
  assert.deepEqual(result.bridges,[[{time:origin,value:170},{time:origin+30*60000,value:130}]]);
  assert.equal(result.segments.length,2);
  assert.equal(result.projection.length,0);
  for(const change of ['reset','plan','unknown']) {
    const changed=structuredClone(after);
    if(change==='reset') changed[0].windows[0].reset_at=new Date(origin+10*3600000).toISOString();
    if(change==='plan') changed[0].plan='Max';
    if(change==='unknown') delete changed[0].windows[0].reset_at;
    assert.deepEqual(buildPool([...before,...changed],accounts,'5h',origin+30*60000,300).bridges,[]);
  }
});
