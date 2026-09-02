const EFFORT_BOLTS = { low: 1, medium: 2, high: 3, xhigh: 4, max: 5 };
const TRUNK_BRANCHES = ['main', 'master', 'dev'];

export function contextTone(percent) {
  if (percent >= 85) return 'bad';
  if (percent >= 60) return 'warn';
  return 'good';
}

function formatTokens(n) {
  if (!n) return '';
  if (n >= 1e6) return `${(n / 1e6).toFixed(1).replace(/\.0$/, '')}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

function gitItems(checkout, mainBranch) {
  if (!checkout || checkout.checkoutKnown === false) return [];
  const items = [];
  if (checkout.changesKnown) {
    const dirty = (checkout.staged | 0) + (checkout.modified | 0) + (checkout.untracked | 0) + (checkout.conflicted | 0);
    const onTrunk = !!checkout.branch && (checkout.branch === mainBranch || TRUNK_BRANCHES.includes(checkout.branch));
    if (dirty > 0 || checkout.clean === false) {
      items.push({ key: 'git', text: `📝 uncommitted${dirty ? ` ${dirty}` : ''}`, tone: 'warn', title: `${dirty || 'Offene'} Änderungen in diesem Checkout sind noch nicht committet` });
    } else if (onTrunk) {
      items.push({ key: 'git', text: '✅ clear', tone: 'good', title: 'Keine offenen Änderungen' });
    } else {
      items.push({ key: 'git', text: '💾 committed', tone: 'info', title: `Alles committet — Branch ${checkout.branch || ''} ist noch nicht auf dem Hauptbranch` });
    }
  }
  if (checkout.divergenceKnown) {
    if (checkout.ahead > 0) items.push({ key: 'ahead', text: `⇡ ${checkout.ahead}`, tone: 'good', title: `${checkout.ahead} Commits vor dem Upstream` });
    if (checkout.behind > 0) items.push({ key: 'behind', text: `⇣ ${checkout.behind}`, tone: 'warn', title: `${checkout.behind} Commits hinter dem Upstream` });
  }
  return items;
}

function runItems(line) {
  if (!line) return [];
  const items = [];
  const percent = Math.max(0, Math.min(100, Math.round(line.contextPercent ?? 0)));
  const window = formatTokens(line.contextWindow);
  const used = formatTokens(line.contextTokens);
  items.push({
    key: 'context', text: `🧠 ${percent}%`, tone: contextTone(percent), meter: percent,
    title: `Kontextfenster ${percent}% belegt${used ? ` · ${used}${window ? ` von ${window}` : ''} Tokens` : ''}`,
  });
  if (line.model) {
    items.push({ key: 'model', text: `🤖 ${line.model}${line.fastMode ? ' · fast' : ''}`, title: `Modell ${line.model}${line.fastMode ? ' im Fast Mode' : ''}${line.version ? ` · Claude Code ${line.version}` : ''}` });
  }
  if (line.effort) {
    items.push({ key: 'effort', text: `${'⚡'.repeat(EFFORT_BOLTS[line.effort] || 1)} ${line.effort}`, title: `Effort ${line.effort}` });
  }
  if (line.costUsd > 0) {
    items.push({ key: 'cost', text: `$${line.costUsd.toFixed(2)}`, title: 'Kosten dieser Session laut Claude Code' });
  }
  return items;
}

// statusLineItems turns what the app knows about the active Session into the
// chips of the line under the terminal: the checkout's Git state first, then
// the run facts the agent's own status line used to show. A Session that is
// gone keeps its Git chips; its last reported model or context would only
// look current.
export function statusLineItems(agent, { gone = false } = {}) {
  if (!agent) return [];
  return [
    ...gitItems(agent.checkout, agent.mainBranch),
    ...(gone ? [] : runItems(agent.statusLine)),
  ];
}
