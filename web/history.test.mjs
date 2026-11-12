import {test} from 'node:test';
import assert from 'node:assert/strict';
import {buildAccounts, buildSeries, resetMarkers, rangeStart} from './history.mjs';

test('Today starts at midnight IST, including across the UTC date boundary', () => {
  assert.equal(new Date(rangeStart(Date.parse('2026-11-12T05:45:00Z'),'today')).toISOString(),'2026-11-11T18:30:00.000Z');
  assert.equal(new Date(rangeStart(Date.parse('2026-11-11T19:00:00Z'),'today')).toISOString(),'2026-11-11T18:30:00.000Z');
});

// row creates a saved poll within one known allowance period.
function row(minute, left, extra = {}) {
  return {at:new Date(Date.UTC(2026,10,10,0,minute)).toISOString(), epoch:'enabled', every_s:300, plan:'Pro', windows:[{id:'five_hour', kind:'5h', left, reset_at:'2026-11-10T05:00:00Z'}], ...extra};
}

test('combined history keeps account drains separate and preserves empty accounts', () => {
  const accounts = [{key:'a',provider:'claude',label:'First'}, {key:'b',provider:'codex',label:'Second'}, {key:'c',provider:'claude',label:'Waiting'}];
  const rows = [row(5,.6,{account:'a'}),row(0,.9,{account:'b'}),row(0,.8,{account:'a'}),row(5,.85,{account:'b'})];
  const groups = buildAccounts(rows,accounts,'5h');
  assert.equal(groups.length,3);
  assert.ok(Math.abs(groups[0].series.drop-20)<.001);
  assert.ok(Math.abs(groups[1].series.drop-5)<.001);
  assert.equal(groups[2].series.observations.length,0);
  assert.equal(buildSeries([row(0,.9,{account:'a'}),row(5,.2,{account:'b'})],'5h').drop,0);
});

test('unchanged readings remain observed and drops use percentage points', () => {
  const result=buildSeries([row(0,.6),row(5,.6),row(10,.4),row(15,.45)],'5h');
  assert.equal(result.segments.length,1);
  assert.equal(result.observations.length,4);
  assert.ok(Math.abs(result.drop-20)<.001);
  assert.ok(Math.abs(result.increase-5)<.001);
});

test('failures, missing windows, inferred values and recording pauses break lines', () => {
  for (const gap of [row(5,0,{failure:'fetch_failed'}),row(5,0,{windows:[]}),row(5,0,{windows:[{kind:'5h',left:0,inferred:true}]})]) {
    const result=buildSeries([row(0,.6),gap,row(10,.4)],'5h');
    assert.equal(result.segments.length,2);
    assert.equal(result.drop,0);
  }
  assert.equal(buildSeries([row(0,.6),row(5,.4,{epoch:'re-enabled'})],'5h').segments.length,2);
  assert.equal(buildSeries([row(0,.6),row(30,.4)],'5h').segments.length,2);
});

test('resets and plan changes never count as drain or assumed refills', () => {
  const reset=row(5,.2,{windows:[{id:'five_hour',kind:'5h',left:.2,reset_at:'2026-11-10T10:00:00Z'}]});
  for (const next of [reset,row(5,.4,{plan:'Max'}),row(5,.4,{windows:[{kind:'5h',left:.4}]})]) {
    const result=buildSeries([row(0,.6),next],'5h');
    assert.equal(result.segments.length,2);
    assert.equal(result.drop,0);
    assert.equal(result.increase,0);
  }
});

test('relative reset timestamps tolerate small drift', () => {
  const a=row(0,.6),b=row(5,.5);
  a.windows[0].reset_estimated=true;
  b.windows[0].reset_estimated=true;
  b.windows[0].reset_at='2026-11-10T05:00:30Z';
  assert.equal(buildSeries([a,b],'5h').segments.length,1);
});

test('a measured increase at a changed reset stays separate from drain', () => {
  const next=row(5,.8,{windows:[{id:'five_hour',kind:'5h',left:.8,reset_at:'2026-11-10T10:00:00Z'}]});
  const result=buildSeries([row(0,.2),next],'5h');
  assert.equal(result.segments.length,2);
  assert.equal(result.drop,0);
  assert.ok(Math.abs(result.increase-60)<.001);
});

test('reset markers keep reported times and collapse drift without inferring a refill', () => {
  const estimated = row(10,.4);
  estimated.windows[0].reset_estimated = true;
  estimated.windows[0].reset_at = '2026-11-10T05:00:30Z';
  const failed = row(15,0,{failure:'fetch_failed'});
  failed.windows[0].reset_at = '2026-11-10T06:00:00Z';
  const inferred = row(20,0);
  inferred.windows[0].inferred = true;
  inferred.windows[0].reset_at = '2026-11-10T07:00:00Z';
  const series = buildSeries([row(0,.6),row(5,.5),estimated,failed,inferred],'5h');
  const markers = resetMarkers(series,Date.parse('2026-11-10T00:00:00Z'),Date.parse('2026-11-10T06:30:00Z'));
  assert.deepEqual(markers,[{time:Date.parse('2026-11-10T05:00:00Z'),estimated:false}]);
  assert.deepEqual(resetMarkers(series,Date.parse('2026-11-10T06:00:00Z'),Date.parse('2026-11-10T08:00:00Z')),[]);
});
