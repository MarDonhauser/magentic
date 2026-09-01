export const RESOLVED_FLASH_MS = 900;

export function idleNotchState() {
  return { phase: 'idle', event: null, resolvedOptionId: null };
}

export function receiveNotchEvent(state, event) {
  return { ...state, phase: 'expanded', event, resolvedOptionId: null };
}

export function clearNotchState(state, id) {
  if (id && state.event?.id !== id) return state;
  return idleNotchState();
}

export function resolveNotchState(state, optionId) {
  if (!state.event || state.phase !== 'expanded') return state;
  return { ...state, phase: 'resolved', resolvedOptionId: optionId };
}
