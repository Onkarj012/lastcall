export function quotaSlots(windows) {
  const tightest = kind => windows.filter(w => w.kind === kind)
    .reduce((a, b) => !a || b.left < a.left ? b : a, null);
  return [tightest('5h'), tightest('week') ?? tightest('month')];
}

export function quotaGroups(windows) {
  const groups = new Map();
  for (const window of windows) {
    const id = window.group_id || window.group || 'unknown';
    if (!groups.has(id)) groups.set(id, { id, name: window.group || 'Other quota', windows: [] });
    groups.get(id).windows.push(window);
  }
  return [...groups.values()].sort((a, b) => {
    const gemini = group => group.windows.some(w => w.id?.toLowerCase().startsWith('gemini-'));
    return Number(gemini(b)) - Number(gemini(a)) || a.name.localeCompare(b.name);
  });
}
