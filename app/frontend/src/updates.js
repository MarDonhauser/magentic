// Sparkle presents the native download/install flow. This view stays useful
// for development builds and bridge failures as well as release builds.
export function updateView(status, busy = false, error = '') {
  return {
    version: status?.version || (error ? 'Version nicht verfügbar' : 'Version wird geladen…'),
    message: error || status?.reason || (!status ? 'Update-Funktion wird geladen.' : status?.sessionInProgress
      ? 'Das Update-Fenster ist geöffnet. Dort kannst du das Update laden und installieren.'
      : 'Suche nach einer neuen Version. Vor der Installation bestätigst du den Neustart.'),
    label: busy ? 'Update-Fenster wird geöffnet…' : status?.sessionInProgress ? 'Update-Fenster öffnen' : 'Nach Updates suchen',
    disabled: busy || !status?.enabled || !status?.canCheck,
    error: Boolean(error),
  };
}

export function mountUpdates(container, api) {
  const section = document.createElement('section');
  section.className = 'card app-updates';
  section.setAttribute('aria-labelledby', 'app-updates-title');
  section.innerHTML = '<div class="card-head"><h2 id="app-updates-title">App-Updates</h2></div>' +
    '<p class="update-version"></p><p class="update-message" role="status" aria-live="polite"></p>' +
    '<button type="button" class="btn update-check" disabled>Nach Updates suchen</button>';
  container.appendChild(section);
  const version = section.querySelector('.update-version');
  const message = section.querySelector('.update-message');
  const button = section.querySelector('button');
  let status = null;
  let busy = false;
  let error = '';
  let refreshTimer;

  function render() {
    const state = updateView(status, busy, error);
    version.textContent = state.version === 'Entwicklungsbuild' ? state.version : `magentic · ${state.version}`;
    message.textContent = state.message;
    message.classList.toggle('update-error', state.error);
    button.textContent = state.label;
    button.disabled = state.disabled;
    section.setAttribute('aria-busy', String(busy));
  }

  async function load() {
    try {
      status = await api.Updates();
    } catch {
      error = 'Der Update-Status konnte nicht geladen werden. Öffne die Einstellungen erneut, um es noch einmal zu versuchen.';
    }
    render();
    clearTimeout(refreshTimer);
    if (status?.sessionInProgress && section.isConnected) {
      refreshTimer = setTimeout(() => { if (section.isConnected && !busy) load(); }, 2000);
    }
  }

  button.onclick = async () => {
    if (busy || button.disabled) return;
    busy = true;
    error = '';
    render();
    try {
      await api.CheckForUpdates();
      status = await api.Updates();
    } catch (err) {
      error = String(err?.message || err || 'Update nicht möglich. Bitte versuche es erneut.');
    } finally {
      busy = false;
      render();
      if (status?.sessionInProgress) load();
    }
  };
  render();
  return load();
}
