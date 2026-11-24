import { test } from 'node:test';
import assert from 'node:assert/strict';
import { quotaSlots, geminiSlots } from './quota-view.mjs';

test('Antigravity shows only primary Gemini limits', () => {
  const five = { id: 'gemini-5h', kind: '5h', left: .7 };
  const weekly = { id: 'gemini-weekly', kind: 'week', left: .4 };
  const windows = [
    { id: '3p-weekly', kind: 'week', left: 0 },
    { id: '3p-5h', kind: '5h', left: 1 },
    { id: 'gemini-argon-wtu-weekly', kind: 'week', left: .9 },
    five, weekly,
  ];
  assert.deepEqual(geminiSlots(windows), [five, weekly]);
  assert.deepEqual(geminiSlots(windows.slice(0, 3)), [null, null]);
});

test('a missing five-hour window stays missing instead of using weekly quota', () => {
  const weekly = { id: 'gemini-weekly', kind: 'week', left: .5 };
  assert.deepEqual(quotaSlots([weekly]), [null, weekly]);
  assert.deepEqual(geminiSlots([weekly]), [null, weekly]);
});
