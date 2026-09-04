// Die Anordnung der Sitzungsliste: aus der aufgelösten Ablage des Backends und
// den Live-Daten der Übersicht wird ein Baum, aus dem Baum werden Zeilen, und
// aus einem Drop wird der Zug, den das Backend versteht.

const TOP = '';

function sessionsOf(ov) {
  const agents = new Map();
  for (const p of ov?.projects || []) {
    for (const wt of p.worktrees || []) {
      for (const a of wt.agents || []) {
        // core entscheidet, was lebt (OvAgent.live); eine resumable Session
        // gehört weiter in ihre Projektgruppe.
        if (a.dock || !(a.live || a.resumable)) continue;
        agents.set(String(a.id), {
          ...a,
          project: p.name,
          projectId: String(p.id),
          branch: a.branch || wt.branch || '',
        });
      }
    }
  }
  return agents;
}

// buildSidebar löst ov.sidebar gegen die Live-Daten auf. Was das Backend
// anordnet, aber nicht mehr existiert, fällt weg; was es noch nicht kennt,
// hängt hinten an seiner Standardstelle.
export function buildSidebar(ov) {
  const projects = new Map();
  for (const p of ov?.projects || []) projects.set(String(p.id), p);
  const agents = sessionsOf(ov);

  const placedProjects = new Set();
  const placedSessions = new Set();

  const node = n => {
    const ref = String(n.ref || '');
    if (n.kind === 'divider') {
      return { kind: 'divider', ref, name: n.name || '', collapsed: !!n.collapsed, children: nodes(n.children) };
    }
    if (n.kind === 'project') {
      const project = projects.get(ref);
      if (!project) return null;
      placedProjects.add(ref);
      return { kind: 'project', ref, project, children: nodes(n.children) };
    }
    if (n.kind === 'session') {
      const agent = agents.get(ref);
      if (!agent) return null;
      placedSessions.add(ref);
      return { kind: 'session', ref, agent };
    }
    return null;
  };
  const nodes = list => (list || []).map(node).filter(Boolean);

  const tree = nodes(ov?.sidebar);

  const projectNode = new Map();
  const walk = items => {
    for (const item of items) {
      if (item.kind === 'project') projectNode.set(item.ref, item);
      if (item.children) walk(item.children);
    }
  };
  walk(tree);

  for (const [id, project] of projects) {
    if (placedProjects.has(id)) continue;
    const item = { kind: 'project', ref: id, project, children: [] };
    projectNode.set(id, item);
    tree.push(item);
  }
  for (const [id, agent] of agents) {
    if (placedSessions.has(id)) continue;
    const parent = projectNode.get(agent.projectId);
    if (parent) parent.children.push({ kind: 'session', ref: id, agent });
    else tree.push({ kind: 'session', ref: id, agent });
  }

  // Ein Projekt ohne Pfad und ohne Sessions ist ein Überbleibsel und
  // verschwindet, so wie es die Liste schon immer gehandhabt hat.
  return tree.filter(item =>
    item.kind !== 'project' || item.project.path || item.children.length);
}

// flattenSidebar macht aus dem Baum die Zeilen, die wirklich zu sehen sind.
// Kinder eines zugeklappten Dividers bleiben draußen.
export function flattenSidebar(tree, parentKind = TOP, parent = '', depth = 0) {
  const rows = [];
  for (const item of tree || []) {
    rows.push({ ...item, parentKind, parent, depth });
    if (item.kind === 'divider') {
      if (!item.collapsed) rows.push(...flattenSidebar(item.children, 'divider', item.ref, depth + 1));
    } else if (item.kind === 'project') {
      // Sessions ruecken unter ihrem Projekt nicht ein: die Liste sah schon
      // immer so aus, und eingerueckt wird nur, was in einem Divider liegt.
      rows.push(...flattenSidebar(item.children, 'project', item.ref, depth));
    }
  }
  return rows;
}

// canPlace hält die Verschachtelung flach: Divider bleiben oben, ein Projekt
// reicht eine Ebene tief, und eine Session kommt nur in ihr eigenes Projekt.
export function canPlace(item, parentKind, parent) {
  if (item.kind === 'divider') return parentKind === TOP;
  if (item.kind === 'project') return parentKind === TOP || parentKind === 'divider';
  if (item.kind === 'session') {
    if (parentKind === TOP || parentKind === 'divider') return true;
    return parentKind === 'project' && parent === item.agent.projectId;
  }
  return false;
}

function levelOf(tree, parentKind, parent) {
  if (parentKind === TOP) return tree;
  for (const item of tree) {
    if (item.kind === 'divider' && parentKind === 'divider' && item.ref === parent) return item.children;
    if (item.kind === 'project' && parentKind === 'project' && item.ref === parent) return item.children;
    if (item.kind === 'divider') {
      const found = levelOf(item.children, parentKind, parent);
      if (found) return found;
    }
  }
  return null;
}

// planMove beschreibt den Zug so, wie das Backend ihn erwartet: Zielebene plus
// deren vollständige neue Reihenfolge. Gibt null zurück, wenn der Zug nichts
// ändert oder dort nicht erlaubt ist.
export function planMove(tree, item, parentKind, parent, index) {
  if (!item || !canPlace(item, parentKind, parent)) return null;
  const level = levelOf(tree, parentKind, parent);
  if (!level) return null;

  const from = level.findIndex(x => x.kind === item.kind && x.ref === item.ref);
  const order = level.filter((_, i) => i !== from).map(x => ({ kind: x.kind, ref: x.ref }));
  // Der Zielindex zählt die gezogene Zeile noch mit, solange sie in dieser
  // Ebene liegt — nach dem Herausnehmen rutscht alles dahinter eins hoch.
  let at = index;
  if (from >= 0 && from < at) at -= 1;
  at = Math.max(0, Math.min(at, order.length));
  if (from === at) return null;
  order.splice(at, 0, { kind: item.kind, ref: item.ref });

  return { kind: item.kind, ref: item.ref, parentKind, parent, order };
}
