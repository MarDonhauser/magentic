const idleState = feedback => ({ kind: 'idle', feedback: feedback || null });

function sessionRef(agent) {
  return agent ? { id: agent.id, name: agent.name } : null;
}

function normalizeAgent(agent) {
  return {
    ...agent,
    id: String(agent?.id || ''),
    name: String(agent?.name || ''),
  };
}

function defaultErrorText(error) {
  return String(error ?? 'Unbekannter Fehler')
    .replace(/^Error:\s*/i, '')
    .replace(/^Fehler:\s*/i, '')
    .trim() || 'Unbekannter Fehler';
}

export function handoffSourceReason(agent) {
  if (!agent) return 'Session nicht gefunden';
  if (!agent.id) return 'Für diese Session ist keine stabile Session-ID verfügbar';
  if (agent.term && agent.handoffSource !== true) {
    return 'Reine Terminals haben keinen KI-Verlauf zum Übergeben';
  }
  if (agent.handoffSource !== true) {
    return 'Für diese Session ist kein übertragbarer KI-Verlauf bekannt';
  }
  return '';
}

export function handoffTargetReason(sourceId, agent) {
  if (!agent) return 'Zielsession nicht gefunden';
  if (!agent.id) return 'Für dieses Ziel ist keine stabile Session-ID verfügbar';
  if (agent.id === sourceId) return 'Quelle und Ziel müssen verschieden sein';
  if (agent.term && agent.handoffTarget !== true) {
    return 'Kontext kann nur an eine KI-Session übergeben werden';
  }
  if (agent.handoffTarget !== true) {
    return 'Dieses Ziel unterstützt noch keine sichere Kontextübergabe';
  }
  if (agent.status === 'blocked') {
    return `${agent.name} wartet auf eine Eingabe — zuerst den offenen Dialog beantworten`;
  }
  if (['exited', 'dead'].includes(agent.status)) return `${agent.name} läuft nicht mehr`;
  return '';
}

// This is the deterministic core of the interaction. It knows stable session
// identity and async request ordering, but nothing about DOM or pointer events.
export function createHandoffCoordinator({
  submit,
  formatError = defaultErrorText,
  onChange = () => {},
} = {}) {
  if (typeof submit !== 'function') throw new TypeError('submit must be a function');

  let agents = new Map();
  let state = idleState();
  let requestEpoch = 0;
  let feedbackSequence = 0;

  const feedback = (kind, message, details = {}) => ({
    id: ++feedbackSequence,
    kind,
    message,
    ...details,
  });

  const snapshot = () => {
    const next = { ...state };
    if (state.sourceId) next.source = sessionRef(agents.get(state.sourceId)) || state.source || null;
    if (state.targetId) next.target = sessionRef(agents.get(state.targetId)) || state.target || null;
    return next;
  };

  const emit = () => onChange(snapshot());

  const reject = (message, sourceId = state.sourceId || '') => {
    const source = agents.get(sourceId);
    const nextFeedback = feedback('error', message);
    state = source
      ? { kind: 'armed', sourceId, source: sessionRef(source), feedback: nextFeedback }
      : idleState(nextFeedback);
    emit();
    return { ok: false, reason: message };
  };

  const reconcile = sessions => {
    agents = new Map((sessions || [])
      .map(normalizeAgent)
      .filter(agent => agent.id)
      .map(agent => [agent.id, agent]));

    if (state.kind !== 'submitting' && state.sourceId && !agents.has(state.sourceId)) {
      requestEpoch += 1;
      state = idleState();
    }
    emit();
    return snapshot();
  };

  const arm = sourceId => {
    if (state.kind === 'submitting') return { ok: false, busy: true };
    const source = agents.get(sourceId);
    const reason = handoffSourceReason(source);
    if (reason) return reject(reason, '');
    state = { kind: 'armed', sourceId, source: sessionRef(source), feedback: null };
    emit();
    return { ok: true };
  };

  const cancel = ({ force = false } = {}) => {
    if (state.kind === 'submitting' && !force) return { ok: false, busy: true };
    requestEpoch += 1;
    state = idleState();
    emit();
    return { ok: true };
  };

  const leave = () => state.kind === 'submitting' ? { ok: false, busy: true } : cancel();

  const submitTarget = async targetId => {
    if (state.kind !== 'armed') return { ok: false, reason: 'Keine Quellsession ausgewählt' };
    const sourceId = state.sourceId;
    const source = agents.get(sourceId);
    const target = agents.get(targetId);
    const reason = handoffTargetReason(sourceId, target);
    if (reason) return reject(reason, sourceId);

    const requestId = ++requestEpoch;
    const sourceSnapshot = sessionRef(source);
    const targetSnapshot = sessionRef(target);
    state = {
      kind: 'submitting',
      sourceId,
      targetId,
      source: sourceSnapshot,
      target: targetSnapshot,
      requestId,
      feedback: null,
    };
    emit();

    try {
      await submit(sourceId, targetId);
      if (requestId !== requestEpoch || state.kind !== 'submitting' || state.requestId !== requestId) {
        return { ok: false, stale: true };
      }
      state = idleState(feedback(
        'success',
        `Kontext von „${sourceSnapshot.name}“ an „${targetSnapshot.name}“ übergeben`,
        { source: sourceSnapshot, target: targetSnapshot },
      ));
      emit();
      return { ok: true };
    } catch (error) {
      if (requestId !== requestEpoch || state.kind !== 'submitting' || state.requestId !== requestId) {
        return { ok: false, stale: true };
      }
      return reject(`Kontextübergabe fehlgeschlagen: ${formatError(error)}`, sourceId);
    }
  };

  const activate = id => {
    if (state.kind === 'submitting') return Promise.resolve({ ok: false, busy: true });
    if (state.kind === 'armed') {
      if (state.sourceId === id) {
        cancel();
        return Promise.resolve({ ok: true, cancelled: true });
      }
      return submitTarget(id);
    }
    return Promise.resolve(arm(id));
  };

  const clearFeedback = id => {
    if (!state.feedback || state.feedback.id !== id) return false;
    state = { ...state, feedback: null };
    emit();
    return true;
  };

  return {
    reconcile,
    snapshot,
    activate,
    arm,
    cancel,
    leave,
    reject,
    submitTarget,
    clearFeedback,
    sourceReason: id => handoffSourceReason(agents.get(id)),
    targetReason: (sourceId, targetId) => handoffTargetReason(sourceId, agents.get(targetId)),
  };
}

function statusPresentation(state, count) {
  const feedback = state.feedback;
  if (state.kind === 'submitting') {
    return {
      active: true,
      busy: true,
      tone: '',
      message: `${state.source?.name || 'Quelle'} → ${state.target?.name || 'Ziel'} wird übergeben …`,
    };
  }
  if (state.kind === 'armed') {
    if (feedback?.kind === 'error') {
      return {
        active: true,
        busy: false,
        tone: 'is-error',
        message: `${feedback.message}. ${state.source?.name || 'Quelle'} bleibt ausgewählt — Ziel erneut wählen · Esc bricht ab`,
      };
    }
    return {
      active: true,
      busy: false,
      tone: '',
      message: `Kontext von ${state.source?.name || 'Quelle'}: Ziel wählen · Esc bricht ab`,
    };
  }
  if (feedback) {
    return {
      active: true,
      busy: false,
      tone: feedback.kind === 'success' ? 'is-success' : 'is-error',
      message: feedback.message,
    };
  }
  return {
    active: false,
    busy: false,
    tone: '',
    message: `${count} ${count === 1 ? 'Session' : 'Sessions'} parallel`,
  };
}

export function createHydraHandoff({
  root,
  statusElement,
  submit,
  notify = () => {},
  renderIcon = () => '',
  formatError = defaultErrorText,
} = {}) {
  if (!root) throw new TypeError('root is required');
  if (typeof statusElement !== 'function') throw new TypeError('statusElement must be a function');

  const doc = root.ownerDocument;
  const win = doc.defaultView || globalThis.window;
  let agentsById = new Map();
  let agentsByName = new Map();
  let agentCount = 0;
  let drag = null;
  let suppressClick = false;
  let sourceTrigger = null;
  let feedbackTimer = null;
  let lastFeedbackId = 0;
  let disposed = false;

  const coordinator = createHandoffCoordinator({
    submit,
    formatError,
    onChange: state => {
      if (disposed) return;
      render(state);
      const currentFeedback = state.feedback;
      if (currentFeedback && currentFeedback.id !== lastFeedbackId) {
        lastFeedbackId = currentFeedback.id;
        notify(currentFeedback.message, currentFeedback.kind === 'error');
        win.clearTimeout(feedbackTimer);
        if (currentFeedback.kind === 'success') {
          feedbackTimer = win.setTimeout(() => coordinator.clearFeedback(currentFeedback.id), 5000);
        }
      }
    },
  });

  function appendIcon(element) {
    const holder = doc.createElement('span');
    holder.innerHTML = renderIcon();
    const rendered = holder.firstElementChild;
    if (rendered) element.appendChild(rendered);
  }

  function updateStatus(state) {
    const element = statusElement();
    if (!element) return;
    const presentation = statusPresentation(state, agentCount);
    element.classList.toggle('is-handoff', presentation.active);
    element.classList.toggle('is-error', presentation.tone === 'is-error');
    element.classList.toggle('is-success', presentation.tone === 'is-success');
    element.setAttribute('role', 'status');
    element.setAttribute('aria-live', 'polite');
    element.setAttribute('aria-atomic', 'true');
    element.setAttribute('aria-busy', String(presentation.busy));
    element.replaceChildren();
    if (presentation.active) appendIcon(element);
    const copy = doc.createElement('span');
    copy.textContent = presentation.message;
    element.appendChild(copy);
  }

  function render(providedState) {
    const state = providedState || coordinator.snapshot();
    const sourceId = state.source?.id || state.sourceId || '';
    const targetId = state.target?.id || state.targetId || '';
    const busy = state.kind === 'submitting';
    const status = statusElement();

    for (const wrap of root.querySelectorAll('.term-wrap')) {
      const id = wrap.dataset.sessionId || '';
      const name = wrap.dataset.termName || '';
      const agent = agentsById.get(id) || agentsByName.get(name);
      const button = wrap.querySelector('.hh-magnet');
      if (!button) continue;
      const isSource = !!sourceId && id === sourceId;
      const sourceReason = handoffSourceReason(agent);
      const targetReason = sourceId ? handoffTargetReason(sourceId, agent) : '';
      const isTarget = !!sourceId && (busy ? id === targetId : !targetReason);
      const unavailable = sourceId ? !!targetReason && !isSource : !!sourceReason;

      wrap.classList.toggle('handoff-source', isSource);
      wrap.classList.toggle('handoff-target', isTarget);
      wrap.classList.toggle('handoff-over', isTarget && id === drag?.overId);
      button.classList.toggle('is-source', isSource);
      button.classList.toggle('is-target', isTarget);
      button.classList.toggle('is-unavailable', unavailable);
      button.disabled = busy;
      button.setAttribute('aria-pressed', String(isSource));
      if (isSource) button.setAttribute('aria-keyshortcuts', 'Escape');
      else button.removeAttribute('aria-keyshortcuts');
      if (status?.id) button.setAttribute('aria-describedby', status.id);
      else button.removeAttribute('aria-describedby');
      button.removeAttribute('aria-disabled');

      if (busy) {
        const running = `${state.source?.name || 'Quelle'} → ${state.target?.name || 'Ziel'} wird übergeben`;
        button.setAttribute('aria-label', id === targetId ? running : `${name}: ${running}`);
        button.title = running;
      } else if (!sourceId) {
        button.setAttribute('aria-label', sourceReason ? `${name}: ${sourceReason}` : `Kontext aus Session ${name} weitergeben`);
        button.title = sourceReason || 'Session-Magnet: auf eine andere KI-Session ziehen oder zum Auswählen aktivieren';
      } else if (isSource) {
        button.setAttribute('aria-label', `Kontextübergabe aus Session ${name} abbrechen`);
        button.title = 'Kontextübergabe abbrechen';
      } else {
        button.setAttribute('aria-label', targetReason ? `${name}: ${targetReason}` : `Kontext aus Session ${state.source?.name || 'Quelle'} an ${name} übergeben`);
        button.title = targetReason || `Kontext aus „${state.source?.name || 'Quelle'}“ hierhin übergeben`;
      }
    }
    updateStatus(state);
  }

  function removePointerListeners() {
    win.removeEventListener('pointermove', onPointerMove);
    win.removeEventListener('pointerup', onPointerUp);
    win.removeEventListener('pointercancel', onPointerCancelled);
  }

  function cleanupPointer() {
    const current = drag;
    removePointerListeners();
    current?.pointerTarget?.removeEventListener('lostpointercapture', onLostPointerCapture);
    if (current?.pointerTarget?.hasPointerCapture?.(current.pointerId)) {
      try { current.pointerTarget.releasePointerCapture(current.pointerId); } catch { /* pointer is already gone */ }
    }
    current?.ghost?.remove();
    drag = null;
    doc.body.classList.remove('session-magnet-dragging');
  }

  function magnetButton(target) {
    const button = target?.closest?.('.hh-magnet');
    return button && root.contains(button) ? button : null;
  }

  function pointerSession(button) {
    const wrap = button?.closest('.term-wrap');
    return {
      id: wrap?.dataset.sessionId || '',
      name: wrap?.dataset.termName || '',
      agent: agentsById.get(wrap?.dataset.sessionId || '') || agentsByName.get(wrap?.dataset.termName || ''),
    };
  }

  function onPointerDown(event) {
    const button = magnetButton(event.target);
    if (!button || event.button !== 0 || coordinator.snapshot().kind === 'submitting') return;
    const session = pointerSession(button);
    const state = coordinator.snapshot();
    if (state.kind === 'armed' && state.source?.id !== session.id) return;
    if (handoffSourceReason(session.agent)) return;

    cleanupPointer();
    event.stopPropagation();
    drag = {
      sourceId: session.id,
      sourceName: session.name,
      pointerId: event.pointerId,
      pointerTarget: button,
      startX: event.clientX,
      startY: event.clientY,
      active: false,
      overId: '',
      ghost: null,
    };
    button.addEventListener('lostpointercapture', onLostPointerCapture);
    try { button.setPointerCapture?.(event.pointerId); } catch { /* capture is an enhancement */ }
    win.addEventListener('pointermove', onPointerMove);
    win.addEventListener('pointerup', onPointerUp);
    win.addEventListener('pointercancel', onPointerCancelled);
  }

  function onPointerMove(event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    if (event.pointerType === 'mouse' && event.buttons === 0) {
      onPointerCancelled(event);
      return;
    }
    if (!drag.active) {
      if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4) return;
      if (!coordinator.arm(drag.sourceId).ok) {
        cleanupPointer();
        return;
      }
      drag.active = true;
      suppressClick = true;
      sourceTrigger = drag.pointerTarget;
      drag.ghost = doc.createElement('div');
      drag.ghost.className = 'session-magnet-ghost';
      appendIcon(drag.ghost);
      const label = doc.createElement('span');
      label.textContent = drag.sourceName;
      drag.ghost.appendChild(label);
      doc.body.appendChild(drag.ghost);
      doc.body.classList.add('session-magnet-dragging');
    }

    event.preventDefault();
    drag.ghost.style.transform = `translate3d(${event.clientX + 12}px, ${event.clientY + 12}px, 0)`;
    const wrap = doc.elementFromPoint(event.clientX, event.clientY)?.closest?.('#hydra-grid .term-wrap');
    const nextId = wrap?.dataset.sessionId || '';
    const validId = nextId && !coordinator.targetReason(drag.sourceId, nextId) ? nextId : '';
    if (validId !== drag.overId) {
      drag.overId = validId;
      render();
    }
  }

  function suppressSyntheticClick() {
    suppressClick = true;
    win.setTimeout(() => { suppressClick = false; }, 0);
  }

  function onPointerUp(event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const completedDrag = drag;
    const targetId = drag.overId;
    cleanupPointer();
    if (!completedDrag.active) return;
    suppressSyntheticClick();
    render();
    if (targetId) coordinator.submitTarget(targetId);
    else coordinator.reject('Kein gültiges KI-Terminal getroffen — Ziel erneut wählen oder mit Esc abbrechen', completedDrag.sourceId);
  }

  function onPointerCancelled(event) {
    if (!drag || (event?.pointerId != null && event.pointerId !== drag.pointerId)) return;
    const wasActive = drag.active;
    cleanupPointer();
    if (wasActive) {
      suppressSyntheticClick();
      render();
    }
  }

  function onLostPointerCapture(event) {
    onPointerCancelled(event);
  }

  function onClick(event) {
    const button = magnetButton(event.target);
    if (!button) return;
    event.stopPropagation();
    if (suppressClick || coordinator.snapshot().kind === 'submitting') {
      event.preventDefault();
      return;
    }
    const session = pointerSession(button);
    const state = coordinator.snapshot();
    if (state.kind !== 'armed') sourceTrigger = button;
    const reason = state.kind === 'armed'
      ? handoffTargetReason(state.source?.id || state.sourceId, session.agent)
      : handoffSourceReason(session.agent);
    if (reason) {
      coordinator.reject(reason, state.kind === 'armed' ? state.source?.id || state.sourceId : '');
      return;
    }
    coordinator.activate(session.id);
  }

  function onKeyDown(event) {
    const state = coordinator.snapshot();
    if (event.key !== 'Escape' || (!drag && state.kind === 'idle')) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (state.kind === 'submitting') return;
    const restore = sourceTrigger;
    cleanupPointer();
    suppressClick = false;
    sourceTrigger = null;
    coordinator.cancel();
    if (restore?.isConnected) restore.focus();
  }

  function onWindowBlur() {
    if (drag) onPointerCancelled();
  }

  root.addEventListener('pointerdown', onPointerDown);
  root.addEventListener('click', onClick);
  win.addEventListener('keydown', onKeyDown, { capture: true });
  win.addEventListener('blur', onWindowBlur);

  return {
    reconcile(sessions) {
      const normalized = (sessions || []).map(normalizeAgent);
      agentCount = normalized.length;
      agentsById = new Map(normalized.filter(agent => agent.id).map(agent => [agent.id, agent]));
      agentsByName = new Map(normalized.map(agent => [agent.name, agent]));
      return coordinator.reconcile(normalized);
    },
    leave() {
      win.clearTimeout(feedbackTimer);
      cleanupPointer();
      suppressClick = false;
      sourceTrigger = null;
      coordinator.leave();
    },
    dispose() {
      if (disposed) return;
      cleanupPointer();
      win.clearTimeout(feedbackTimer);
      root.removeEventListener('pointerdown', onPointerDown);
      root.removeEventListener('click', onClick);
      win.removeEventListener('keydown', onKeyDown, { capture: true });
      win.removeEventListener('blur', onWindowBlur);
      disposed = true;
      coordinator.cancel({ force: true });
    },
  };
}
