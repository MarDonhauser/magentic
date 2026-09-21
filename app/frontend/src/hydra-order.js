export function createHydraOrder() {
  const remembered = new Map();
  return {
    order(project, agents) {
      const known = remembered.get(project) || [];
      const pending = new Map();
      for (const agent of agents || []) {
        if (!agent || typeof agent.name !== 'string' || pending.has(agent.name)) continue;
        pending.set(agent.name, agent);
      }
      const ordered = [];
      for (const name of known) {
        const agent = pending.get(name);
        if (!agent) continue;
        ordered.push(agent);
        pending.delete(name);
      }
      for (const agent of pending.values()) ordered.push(agent);
      if (pending.size) remembered.set(project, [...known, ...pending.keys()]);
      return ordered;
    },
    forget(project) {
      remembered.delete(project);
    },
  };
}

export function placeInOrder(container, elements) {
  let slot = container.firstElementChild;
  for (const element of elements) {
    if (element === slot) {
      slot = slot.nextElementSibling;
      continue;
    }
    container.insertBefore(element, slot);
  }
}
