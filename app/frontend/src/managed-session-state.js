export function emptyManagedSessionState() {
  return {
    availability: 'not-managed',
    reason: '',
    turnRunning: false,
    permission: null,
  };
}

export function applyManagedSessionState(result) {
  if (!result || typeof result !== 'object') return emptyManagedSessionState();
  const permission = result.permission && typeof result.permission === 'object'
    ? {
        id: String(result.permission.id || ''),
        asked: String(result.permission.asked || ''),
        raisedAt: String(result.permission.raisedAt || ''),
        open: result.permission.open === true,
        outcome: String(result.permission.outcome || ''),
        closeReason: String(result.permission.closeReason || ''),
      }
    : null;
  return {
    availability: String(result.availability || 'unavailable'),
    reason: String(result.reason || ''),
    turnRunning: result.turnRunning === true,
    permission,
  };
}

export function managedControlModel(state) {
  const current = state || emptyManagedSessionState();
  if (current.availability === 'not-managed') {
    return { visible: false, tone: '', title: '', detail: '', actions: [] };
  }
  if (current.availability !== 'available') {
    return {
      visible: true,
      tone: 'unavailable',
      title: 'Agent-Steuerung nicht erreichbar',
      detail: current.reason || 'Der verwaltete Agent kann gerade nicht erreicht werden.',
      actions: [],
    };
  }
  if (current.permission?.open) {
    return {
      visible: true,
      tone: 'permission',
      title: 'Freigabe erforderlich',
      detail: current.permission.asked || 'Der Agent wartet auf deine Entscheidung.',
      requestID: current.permission.id,
      actions: [
        { kind: 'deny-permission', label: 'Ablehnen', tone: 'secondary' },
        { kind: 'allow-permission', label: 'Erlauben', tone: 'primary' },
      ],
    };
  }
  if (current.turnRunning) {
    return {
      visible: true,
      tone: 'running',
      title: 'Agent arbeitet',
      detail: 'Der aktuelle Arbeitsschritt kann unterbrochen werden, ohne die Session zu beenden.',
      actions: [{ kind: 'interrupt-turn', label: 'Unterbrechen', tone: 'secondary' }],
    };
  }
  return { visible: false, tone: '', title: '', detail: '', actions: [] };
}
