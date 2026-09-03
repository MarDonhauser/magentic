// Nur zur Sichtprüfung: rendert die echte Übersicht mit festen Beispieldaten,
// indem es die Wails-Brücke stubt und danach main.js unverändert lädt. Die
// Daten bilden den Zustand ab, der die Karten am dichtesten macht — ein
// Repository ohne lesbare Git-Fakten neben einem vollständig gelesenen.

const overview = {
  projects: [
    {
      id: 'p-navi', name: 'NAVI', path: '/Users/U751725/NAVI/NAVI',
      mainBranch: 'main', mainBranchKnown: true, repositoryKnowledge: 'partial',
      worktrees: [
        {
          isMain: true, branch: 'main', location: '/Users/U751725/NAVI/NAVI', reference: 'wt-main',
          checkoutKnown: true, changesKnown: true, divergenceKnown: true,
          clean: false, modified: 1, ahead: 0, behind: 0,
          warnings: ['uncommitted Änderungen, keine aktive Session'], agents: [],
        },
        ...['scratchpad/employer-polish', 'scratchpad/merge-sync', 'scratchpad/navi-task2', 'tmp/navi-ui-motion.wZ3BAx']
          .map((location, i) => ({
            isMain: false, reference: 'wt-' + i,
            branch: ['(detached)', 'merge/search-motion-sync', 'fix/candidate-search-selection-task2', 'codex/ui-motion-pass'][i],
            location, checkoutKnown: true, changesKnown: false, divergenceKnown: false,
            clean: false, ahead: 0, behind: 0, agents: [], warnings: [],
            problems: [{ message: 'git status: exit 128 — dubious ownership in repository' }],
          })),
      ],
    },
    {
      id: 'p-magentic', name: 'magentic', path: '/Users/U751725/magentic',
      mainBranch: 'main', mainBranchKnown: true, repositoryKnowledge: 'known',
      worktrees: [
        {
          isMain: true, branch: 'main', location: '/Users/U751725/magentic', reference: 'wt-m-main',
          checkoutKnown: true, changesKnown: true, divergenceKnown: true,
          clean: false, modified: 1, ahead: 0, behind: 0, warnings: [],
          agents: [
            { id: 'a1', name: 'magentic-2', status: 'exited', age: '18m', project: 'magentic', vendor: 'claude' },
            { id: 'a2', name: 'multi-provider-agent', status: 'exited', age: '17m', project: 'magentic', vendor: 'claude' },
            { id: 'a3', name: 'multi-provider-agent-2', status: 'done', age: '2h29m', project: 'magentic', vendor: 'claude' },
            { id: 'a4', name: 'magentic', status: 'exited', age: '2d', project: 'magentic', vendor: 'claude' },
            { id: 'a5', name: 'magentic-3', status: 'blocked', age: 'jetzt', project: 'magentic', vendor: 'claude', unread: true },
          ],
        },
        {
          isMain: false, branch: 'feature/workhistory-index', reference: 'wt-m-wh',
          location: 'worktrees/workhistory-index',
          checkoutKnown: true, changesKnown: true, divergenceKnown: true,
          clean: false, modified: 3, untracked: 2, ahead: 17, behind: 46, agents: [],
          warnings: ['uncommitted Änderungen, keine aktive Session', '17 Commits nicht in main'],
        },
      ],
    },
  ],
  problems: [],
};

// ?state=unknown zeigt denselben Bildschirm, wenn Magentic den Zustand seiner
// Sessions nicht lesen kann — der zweite Fall, den die Übersicht tragen muss.
if (new URLSearchParams(location.search).get('state') === 'unknown') {
  for (const project of overview.projects) {
    for (const wt of project.worktrees) {
      for (const agent of wt.agents || []) {
        agent.status = 'unknown';
        agent.unread = false;
      }
    }
  }
}

const fixtures = {
  Overview: () => overview,
  Inbox: () => ({ entries: [] }),
  BuildInfo: () => ({ version: 'dev', commit: 'preview' }),
  Breaks: () => ({ state: 'running', minutes: 13 }),
  DeployStatus: () => ({ builds: [], apps: [] }),
  Zeitgeist: () => ({ projects: [] }),
  NotificationsEnabled: () => true,
  MigrateDockSessions: () => [],
  PromptLinePattern: () => '',
};

const app = new Proxy({}, {
  get: (_, name) => (...args) => Promise.resolve(fixtures[name] ? fixtures[name](...args) : null),
});
window.go = { main: { App: app } };
window.runtime = new Proxy({}, { get: () => () => {} });

// main.js greift beim Laden auf das Markup der Anwendung zu. Die Vorschau holt
// es aus index.html, statt eine zweite Fassung zu pflegen, die veraltet.
const shell = await (await fetch('/index.html')).text();
const parsed = new DOMParser().parseFromString(shell, 'text/html');
for (const script of parsed.querySelectorAll('script')) script.remove();
document.body.replaceChildren(...parsed.body.childNodes);

await import('./main.js');
