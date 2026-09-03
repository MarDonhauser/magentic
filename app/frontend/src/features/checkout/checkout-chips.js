const TRUNK_BRANCHES = ['main', 'master', 'dev'];

export function checkoutChips(agent) {
  const checkout = agent?.checkout;
  if (!checkout || checkout.checkoutKnown === false) return [];
  const chips = [];
  if (checkout.changesKnown) {
    const dirty = (checkout.staged | 0) + (checkout.modified | 0) + (checkout.untracked | 0) + (checkout.conflicted | 0);
    const mainBranch = agent.mainBranch;
    const onTrunk = !!checkout.branch && (checkout.branch === mainBranch || TRUNK_BRANCHES.includes(checkout.branch));
    if (dirty > 0 || checkout.clean === false) {
      chips.push({ key: 'git', text: `📝 uncommitted${dirty ? ` ${dirty}` : ''}`, tone: 'warn', title: `${dirty || 'Offene'} Änderungen in diesem Checkout sind noch nicht committet` });
    } else if (onTrunk) {
      chips.push({ key: 'git', text: '✅ clear', tone: 'good', title: 'Keine offenen Änderungen' });
    } else {
      chips.push({ key: 'git', text: '💾 committed', tone: 'info', title: `Alles committet — Branch ${checkout.branch || ''} ist noch nicht auf dem Hauptbranch` });
    }
  }
  if (checkout.divergenceKnown) {
    if (checkout.ahead > 0) chips.push({ key: 'ahead', text: `⇡ ${checkout.ahead}`, tone: 'plain', title: `${checkout.ahead} Commits vor dem Upstream` });
    if (checkout.behind > 0) chips.push({ key: 'behind', text: `⇣ ${checkout.behind}`, tone: 'plain', title: `${checkout.behind} Commits hinter dem Upstream` });
  }
  return chips;
}
