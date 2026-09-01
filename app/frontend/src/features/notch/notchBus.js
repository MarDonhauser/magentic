/** @import { NotchEvent, NotchResponse } from './types.js' */

export const NOTCH_EVENT = 'notch://event';
export const NOTCH_CLEAR = 'notch://clear';
export const NOTCH_RESPONSE = 'notch://response';

function dispatch(name, detail) {
  window.dispatchEvent(new CustomEvent(name, { detail }));
}

function appBinding(method, ...args) {
  const binding = window.go?.main?.App?.[method];
  if (typeof binding !== 'function') return Promise.resolve();
  return binding(...args);
}

/** @param {NotchEvent} event */
export function showNotchEvent(event) {
  return appBinding('ShowNotchEvent', event);
}

/** @param {string} [id] */
export function clearNotch(id) {
  return appBinding('ClearNotch', id ?? '');
}

/** @param {NotchResponse} response */
export function respondToNotch(response) {
  const nativeHandler = window.webkit?.messageHandlers?.notchResponse;
  if (nativeHandler) {
    nativeHandler.postMessage(response);
    return Promise.resolve();
  }
  return appBinding('RespondToNotch', response);
}

/** @param {(event: NotchEvent) => void} handler */
export function onNotchEvent(handler) {
  const listener = event => handler(event.detail);
  window.addEventListener(NOTCH_EVENT, listener);
  return () => window.removeEventListener(NOTCH_EVENT, listener);
}

/** @param {(id?: string) => void} handler */
export function onNotchClear(handler) {
  const listener = event => handler(event.detail?.id);
  window.addEventListener(NOTCH_CLEAR, listener);
  return () => window.removeEventListener(NOTCH_CLEAR, listener);
}

/** @param {(response: NotchResponse) => void} handler */
export function onNotchResponse(handler) {
  const listener = event => handler(event.detail);
  window.addEventListener(NOTCH_RESPONSE, listener);
  return () => window.removeEventListener(NOTCH_RESPONSE, listener);
}

// AppKit injects cross-window messages through this narrow DOM seam. The
// state machine itself stays independent from Wails and from the native shell.
window.__magenticNotchDispatch = dispatch;
