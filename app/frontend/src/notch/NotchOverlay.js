import { onNotchClear, onNotchEvent, respondToNotch } from '../features/notch/notchBus.js';
import {
  clearNotchState,
  idleNotchState,
  receiveNotchEvent,
  RESOLVED_FLASH_MS,
  resolveNotchState,
} from '../features/notch/state.js';

const ICONS = {
  permission: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/><path d="M12 8v4"/><path d="M12 16h.01"/>',
  question: '<circle cx="12" cy="12" r="10"/><path d="M9.1 9a3 3 0 1 1 5.83 1c0 2-3 2-3 4"/><path d="M12 18h.01"/>',
  review: '<circle cx="12" cy="12" r="10"/><path d="m8 12 2.7 2.7L16 9.4"/>',
};

function icon(kind) {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[kind]}</svg>`;
}

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>"']/g, char => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[char]);
}

function setInteractive(interactive) {
  window.webkit?.messageHandlers?.notchInteractive?.postMessage({ interactive });
}

export function mountNotchOverlay(root) {
  let state = idleNotchState();
  let resolveTimer = null;

  function cancelResolveTimer() {
    if (resolveTimer !== null) window.clearTimeout(resolveTimer);
    resolveTimer = null;
  }

  function render() {
    const { phase, event, resolvedOptionId } = state;
    root.className = `notch-root notch-root--${phase}`;
    root.setAttribute('aria-live', phase === 'idle' ? 'off' : 'assertive');

    if (phase === 'idle') {
      root.innerHTML = '<div class="notch-shell notch-shell--idle" aria-hidden="true"><span class="notch-idle-mark"></span></div>';
      setInteractive(false);
      return;
    }

    if (phase === 'resolved' && event) {
      const selected = event.options.find(option => option.id === resolvedOptionId);
      root.innerHTML = `<div class="notch-shell notch-shell--resolved" role="status"><svg class="notch-resolved-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12 4 4L19 6"/></svg><span>${escapeHTML(selected?.label ?? 'Erledigt')}</span></div>`;
      setInteractive(true);
      return;
    }

    if (!event) return;
    const actions = event.options.map(option => (
      `<button type="button" class="notch-button notch-button--${escapeHTML(option.tone ?? 'neutral')}" data-option-id="${escapeHTML(option.id)}">${escapeHTML(option.label)}</button>`
    )).join('');
    root.innerHTML = `<section class="notch-shell notch-shell--expanded" role="alertdialog" aria-modal="false" aria-labelledby="notch-title"${event.detail ? ' aria-describedby="notch-detail"' : ''}><div class="notch-card"><div class="notch-card-heading"><span class="notch-card-icon notch-card-icon--${escapeHTML(event.kind)}">${icon(event.kind)}</span><div class="notch-card-copy"><h1 id="notch-title">${escapeHTML(event.title)}</h1>${event.detail ? `<p id="notch-detail">${escapeHTML(event.detail)}</p>` : ''}</div></div><div class="notch-card-actions">${actions}</div></div></section>`;
    setInteractive(true);
    // Keep the safe/deferring action as the initial keyboard target. Approval
    // always requires an explicit move or click.
    window.requestAnimationFrame(() => (
      root.querySelector('.notch-button--deny, [data-option-id="later"], .notch-button')?.focus()
    ));
  }

  async function choose(optionId) {
    if (!state.event || state.phase !== 'expanded') return;
    const response = { id: state.event.id, optionId };
    state = resolveNotchState(state, optionId);
    render();
    await respondToNotch(response).catch(() => {});
    cancelResolveTimer();
    resolveTimer = window.setTimeout(() => {
      state = idleNotchState();
      resolveTimer = null;
      render();
    }, RESOLVED_FLASH_MS);
  }

  root.addEventListener('click', event => {
    const button = event.target.closest('[data-option-id]');
    if (button) choose(button.dataset.optionId);
  });

  root.addEventListener('keydown', event => {
    if (event.key !== 'Escape' || state.phase !== 'expanded') return;
    const fallback = state.event?.options.find(option => option.tone === 'deny')
      ?? state.event?.options.find(option => option.id === 'later')
      ?? state.event?.options.find(option => option.id === 'open');
    if (fallback) {
      event.preventDefault();
      choose(fallback.id);
    }
  });

  const stopEvent = onNotchEvent(incoming => {
    cancelResolveTimer();
    state = receiveNotchEvent(state, incoming);
    render();
  });
  const stopClear = onNotchClear(id => {
    const next = clearNotchState(state, id);
    if (next === state) return;
    cancelResolveTimer();
    state = next;
    render();
  });

  render();
  return () => {
    cancelResolveTimer();
    stopEvent();
    stopClear();
  };
}
