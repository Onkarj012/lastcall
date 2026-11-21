export function quotaSlots(windows) {
  const tightest = kind => windows.filter(w => w.kind === kind)
    .reduce((a, b) => !a || b.left < a.left ? b : a, null);
  return [tightest('5h'), tightest('week') ?? tightest('month')];
}

export function geminiSlots(windows) {
  const primary = windows.filter(w => w.id === 'gemini-5h' || w.id === 'gemini-weekly');
  return quotaSlots(primary);
}
