const idle = () => ({ kind: 'idle' });

export function createVendorSwitchCoordinator({
  chooseContext,
  switchVendor,
  disconnect = () => undefined,
  reconnect = async () => {},
  onChange = () => {},
} = {}) {
  if (typeof chooseContext !== 'function') throw new TypeError('chooseContext must be a function');
  if (typeof switchVendor !== 'function') throw new TypeError('switchVendor must be a function');

  let state = idle();
  let epoch = 0;
  const emit = next => {
    state = next;
    onChange({ ...state });
  };

  async function request(input) {
    if (['confirming', 'switching', 'reconnecting'].includes(state.kind)) {
      return { ok: false, busy: true };
    }
    const requestId = ++epoch;
    emit({ kind: 'confirming', request: { ...input } });

    let mode;
    try {
      mode = await chooseContext(input);
    } catch (error) {
      if (requestId !== epoch) return { ok: false, stale: true };
      emit({ kind: 'error', request: { ...input }, error });
      return { ok: false, error };
    }
    if (requestId !== epoch) return { ok: false, stale: true };
    if (mode !== 'with-history' && mode !== 'without-history') {
      emit(idle());
      return { ok: false, cancelled: true };
    }

    emit({ kind: 'switching', request: { ...input }, mode });
    let disconnectToken;
    let switched = false;
    let failure = null;
    try {
      disconnectToken = await disconnect(input);
      await switchVendor(input.sessionId, input.targetVendor, mode === 'with-history');
      switched = true;
    } catch (error) {
      failure = error;
    }

    emit({ kind: 'reconnecting', request: { ...input }, mode, switched });
    try {
      await reconnect(input, { switched }, disconnectToken);
    } catch (error) {
      failure ||= error;
    }
    if (requestId !== epoch) return { ok: false, stale: true };
    if (failure) {
      emit({ kind: 'error', request: { ...input }, mode, error: failure, switched });
      return { ok: false, error: failure, switched };
    }

    emit({ kind: 'complete', request: { ...input }, mode });
    return { ok: true, mode };
  }

  return {
    request,
    snapshot: () => ({ ...state }),
    reset() {
      epoch += 1;
      emit(idle());
    },
  };
}
