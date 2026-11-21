import { test } from 'node:test';
import assert from 'node:assert/strict';
import { quotaSlots, quotaGroups } from './quota-view.mjs';

test('Antigravity keeps each group and its allowance windows together', () => {
  const windows = [
    { id: 'claude-week', group_id: 'third-party', group: 'Claude and GPT models', kind: 'week', left: 0 },
    { id: 'claude-five', group_id: 'third-party', group: 'Claude and GPT models', kind: '5h', left: 1 },
    { id: 'gemini-week', group_id: 'gemini', group: 'Gemini models', kind: 'week', left: .4 },
    { id: 'gemini-five', group_id: 'gemini', group: 'Gemini models', kind: '5h', left: .7 },
  ];
  const groups = quotaGroups(windows);
  assert.equal(groups.length, 2);
  assert.deepEqual(groups.map(g => g.id), ['gemini', 'third-party']);
  assert.deepEqual(quotaSlots(groups[0].windows).map(w => w.left), [.7, .4]);
  assert.deepEqual(quotaSlots(groups[1].windows).map(w => w.left), [1, 0]);
});

test('a missing five-hour window stays missing instead of using weekly quota', () => {
  const weekly = { kind: 'week', left: .5 };
  assert.deepEqual(quotaSlots([weekly]), [null, weekly]);
});
