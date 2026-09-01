// Projection of the provider quota pages the Overview carries. Providers
// without a readable source send no page at all, so the pager never has to
// render an empty promise.

// usagePages normalizes the raw payload and drops anything that would render
// as a page without bars.
export function usagePages(overview) {
  const raw = Array.isArray(overview?.usage) ? overview.usage : [];
  const out = [];
  for (const page of raw) {
    const provider = typeof page?.provider === 'string' ? page.provider.trim() : '';
    if (!provider) continue;
    const windows = [];
    for (const window of Array.isArray(page.windows) ? page.windows : []) {
      const label = typeof window?.label === 'string' ? window.label.trim() : '';
      const percent = Number(window?.percent);
      if (!label || !Number.isFinite(percent)) continue;
      windows.push({
        label,
        percent: Math.max(0, Math.min(100, Math.round(percent))),
        reset: String(window.reset ?? '').trim(),
      });
    }
    if (!windows.length) continue;
    out.push({ provider, label: String(page.label ?? '').trim() || provider, windows });
  }
  return out;
}

// clampUsagePage keeps a remembered page index valid while providers come and
// go between refreshes.
export function clampUsagePage(index, count) {
  if (!Number.isFinite(count) || count <= 0) return 0;
  if (!Number.isFinite(index) || index < 0) return 0;
  return Math.min(Math.floor(index), count - 1);
}
