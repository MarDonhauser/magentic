# Multi-Provider Agent-Runtime — Implementierungsplan (Teilprojekt 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eine Magentic-Coding-Session kennt ihren Vendor durabel, startet und
setzt ihn über einen Provider-Adapter fort, und kann im Betrieb auf einen
anderen Vendor wechseln.

**Architecture:** Neues Modul `core/provider.go` mit einem `AgentProvider`
je Vendor, registriert wie die vorhandenen WorkHistory-Adapter. Lifecycle,
Observation und Registry fragen die Provider-Registry statt eines
Claude-`switch`. Die Session trägt ein neues `Vendor`-Feld; leer bedeutet
Claude, wodurch jeder bestehende Zustand gültig bleibt.

**Tech Stack:** Go 1.x (Module `magentic` und `app`), tmux als Runtime-Seam,
Wails v2 mit generierten Bindings, Vanilla-JS-Frontend in
`app/frontend/src`, Go-Standardtests (`go test ./core/...`).

**Spec:** `docs/superpowers/specs/2026-09-01-multi-provider-agent-runtime-design.md`

## Global Constraints

- Vier Vendors, exakt die Konstanten aus `core/state.go:15-19`:
  `AgentVendorClaude`, `AgentVendorCodex`, `AgentVendorGemini`,
  `AgentVendorCopilot`. Cursor und Grok sind nicht Teil dieses Plans.
- Belegte Startzeilen (Stand 2026-09-01):
  - claude: neu `claude --name <runtime>`; mit Run-Ref `--session-id <id>`
    bei `mode == "new"`, sonst `--resume <id>`; ohne Run-Ref und
    `mode != "new"` zusätzlich `--continue`.
  - codex: neu `codex`; mit Run-Ref `codex resume <id>`; ohne Run-Ref
    `codex resume --last`.
  - copilot: neu `copilot --name <runtime>`; mit Run-Ref
    `--session-id=<id>` bei `mode == "new"`, sonst `--resume=<id>`; ohne
    Run-Ref und `mode != "new"` zusätzlich `--continue`.
  - gemini: immer `gemini`, keine Fortsetzung.
- Run-Identität: Claude und Copilot nehmen eine vorgegebene UUID an
  (`NewRunID()` liefert eine), Codex und Gemini nicht (`NewRunID()` liefert
  `""`).
- Terminal- und Dock-Sessions haben keinen Vendor. `Session.Vendor` bleibt
  bei ihnen leer, und kein Provider wird für sie aufgelöst.
- Kein neues Konfigurationsformat. Magentic hat nur `state.json`.
- Fehlermeldungen an den Nutzer sind deutsch, wie im übrigen `core`.
- Kommentare und Bezeichner im Go-Code sind englisch, wie im übrigen `core`.
- Tests niemals ungefragt als Gesamtsuite laufen lassen. Jeder Task nennt den
  genauen `go test -run`-Aufruf für seine eigenen Tests.

---

### Task 1: Provider-Modul mit Claude-Adapter

Der Claude-Adapter reproduziert das heutige Verhalten aus
`core/lifecycle.go:1649-1657` exakt. Danach existiert die Registry, aber noch
kein Aufrufer außer `DetectAgentTool`.

**Files:**
- Create: `core/provider.go`
- Modify: `core/status.go:155-176` (`DetectAgentTool` auf die Registry umstellen)
- Test: `core/provider_test.go` (vorhandene Datei erweitern)

**Interfaces:**
- Consumes: `AgentVendor`, `Session`, `AgentRunRef` aus `core/state.go`;
  `AgentToolClaude` und Geschwister aus `core/status.go:144-149`;
  `ShellQuote` und `NewUUID` aus `core/util.go`.
- Produces: `AgentProvider` (Interface), `builtinAgentProviders()`,
  `providerForVendor(AgentVendor) (AgentProvider, bool)`,
  `providerForPaneCommand(string) (AgentProvider, bool)`,
  `providerBinaryAvailable(AgentProvider) bool`,
  `paneCommandMatches(command, binary string) bool`, `claudeProvider`.

- [ ] **Step 1: Write the failing tests**

An `core/provider_test.go` anhängen:

```go
func TestClaudeProviderStartCommand(t *testing.T) {
	session := Session{Name: "navi", RuntimeName: "mgt-navi"}
	provider, ok := providerForVendor(AgentVendorClaude)
	if !ok {
		t.Fatal("kein Claude-Provider registriert")
	}
	run := AgentRunRef{Vendor: AgentVendorClaude, ExternalID: "abc-123"}
	tests := []struct {
		name string
		run  *AgentRunRef
		mode string
		want string
	}{
		{name: "neu ohne Run", mode: "new", want: "claude --name mgt-navi"},
		{name: "neu mit Run", run: &run, mode: "new", want: "claude --name mgt-navi --session-id abc-123"},
		{name: "resume mit Run", run: &run, mode: "resume", want: "claude --name mgt-navi --resume abc-123"},
		{name: "resume ohne Run", mode: "resume", want: "claude --name mgt-navi --continue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider.StartCommand(session, tt.run, tt.mode)
			if err != nil {
				t.Fatalf("StartCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("StartCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderForPaneCommand(t *testing.T) {
	tests := []struct {
		command string
		want    AgentVendor
		found   bool
	}{
		{command: "claude", want: AgentVendorClaude, found: true},
		{command: "-claude", want: AgentVendorClaude, found: true},
		{command: "CLAUDE", want: AgentVendorClaude, found: true},
		{command: "claude-code", want: AgentVendorClaude, found: true},
		{command: "node", found: false},
		{command: "", found: false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			provider, ok := providerForPaneCommand(tt.command)
			if ok != tt.found {
				t.Fatalf("providerForPaneCommand(%q) gefunden = %v, want %v", tt.command, ok, tt.found)
			}
			if ok && provider.Vendor() != tt.want {
				t.Fatalf("providerForPaneCommand(%q) = %q, want %q", tt.command, provider.Vendor(), tt.want)
			}
		})
	}
}

func TestClaudeProviderSuppliesRunID(t *testing.T) {
	provider, _ := providerForVendor(AgentVendorClaude)
	if provider.NewRunID() == "" {
		t.Fatal("Claude nimmt eine vorgegebene Run-Identität an")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestClaudeProvider|TestProviderForPaneCommand' -v`
Expected: FAIL, `undefined: providerForVendor`

- [ ] **Step 3: Create `core/provider.go`**

```go
package core

import (
	"os/exec"
	"strings"
)

// AgentProvider is the Adapter by which one coding-agent vendor is addressed
// as a process. It owns the vendor's command line and nothing else: status
// meaning and prompt semantics stay with Observation and Outbox.
type AgentProvider interface {
	Vendor() AgentVendor
	// Tool is the stable frontend identity already used by Observation and by
	// the developer-icon resolution in the frontend.
	Tool() string
	// Binary must be resolvable on PATH before a Session may start.
	Binary() string
	// Matches recognizes the vendor from the pane command tmux reports. The
	// argument is already lowercased and stripped of a login-shell dash.
	Matches(paneCommand string) bool
	// StartCommand builds the full command line for mode "new" or "resume".
	// run is this vendor's stored AgentRunRef, or nil when none exists.
	StartCommand(session Session, run *AgentRunRef, mode string) (string, error)
	// NewRunID returns a caller-supplied run identity when the vendor accepts
	// one, and "" when the identity can only be discovered afterwards.
	NewRunID() string
}

func builtinAgentProviders() []AgentProvider {
	return []AgentProvider{claudeProvider{}}
}

func providerForVendor(vendor AgentVendor) (AgentProvider, bool) {
	for _, provider := range builtinAgentProviders() {
		if provider.Vendor() == vendor {
			return provider, true
		}
	}
	return nil, false
}

func providerForPaneCommand(paneCommand string) (AgentProvider, bool) {
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(paneCommand)), "-")
	if command == "" {
		return nil, false
	}
	for _, provider := range builtinAgentProviders() {
		if provider.Matches(command) {
			return provider, true
		}
	}
	return nil, false
}

// providerBinaryAvailable reports whether the vendor's executable is on PATH.
// A missing binary is a fail-closed condition: no Session may be started for
// it, and the UI offers it only as unavailable.
func providerBinaryAvailable(provider AgentProvider) bool {
	_, err := exec.LookPath(provider.Binary())
	return err == nil
}

func paneCommandMatches(command, binary string) bool {
	return command == binary || strings.HasPrefix(command, binary+"-")
}

type claudeProvider struct{}

func (claudeProvider) Vendor() AgentVendor { return AgentVendorClaude }
func (claudeProvider) Tool() string        { return AgentToolClaude }
func (claudeProvider) Binary() string      { return "claude" }
func (claudeProvider) NewRunID() string    { return NewUUID() }

func (claudeProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "claude")
}

func (claudeProvider) StartCommand(session Session, run *AgentRunRef, mode string) (string, error) {
	command := "claude --name " + ShellQuote(session.TmuxName())
	if run != nil && run.ExternalID != "" {
		flag := "--resume"
		if mode == "new" {
			flag = "--session-id"
		}
		return command + " " + flag + " " + ShellQuote(run.ExternalID), nil
	}
	if mode != "new" {
		command += " --continue"
	}
	return command, nil
}
```

- [ ] **Step 4: Rewrite `DetectAgentTool` in `core/status.go`**

Den `switch` in `core/status.go:162-174` vollständig ersetzen; die Konstanten
`AgentToolClaude` bis `AgentToolBash` bleiben unverändert stehen.

```go
// DetectAgentTool translates the command tmux reports for a pane into the
// stable frontend identity used by developer-icons. Unknown commands stay
// neutral instead of being mislabeled as Claude.
func DetectAgentTool(paneCommand string, term bool) string {
	if term {
		return AgentToolBash
	}
	if provider, ok := providerForPaneCommand(paneCommand); ok {
		return provider.Tool()
	}
	return ""
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./core/ -run 'TestClaudeProvider|TestProviderForPaneCommand|TestDetectAgentTool' -v`
Expected: `TestDetectAgentTool` schlägt bei `codex`, `gemini` und
`copilot` fehl — die Adapter kommen in Task 2. Alle anderen sind grün.

- [ ] **Step 6: Commit**

```bash
git add core/provider.go core/status.go core/provider_test.go
git commit -m "feat: Provider-Modul mit Claude-Adapter"
```

---

### Task 2: Adapter für Codex, Copilot und Gemini

**Files:**
- Modify: `core/provider.go` (drei Adapter, `builtinAgentProviders` erweitern)
- Test: `core/provider_test.go`

**Interfaces:**
- Consumes: `AgentProvider`, `paneCommandMatches`, `builtinAgentProviders`
  aus Task 1.
- Produces: `codexProvider`, `copilotProvider`, `geminiProvider`.

- [ ] **Step 1: Write the failing tests**

```go
func TestVendorStartCommands(t *testing.T) {
	session := Session{Name: "navi", RuntimeName: "mgt-navi"}
	tests := []struct {
		name     string
		vendor   AgentVendor
		runID    string
		mode     string
		want     string
	}{
		{name: "codex neu", vendor: AgentVendorCodex, mode: "new", want: "codex"},
		{name: "codex resume mit Run", vendor: AgentVendorCodex, runID: "abc-123", mode: "resume", want: "codex resume abc-123"},
		{name: "codex resume ohne Run", vendor: AgentVendorCodex, mode: "resume", want: "codex resume --last"},
		{name: "codex neu mit Run", vendor: AgentVendorCodex, runID: "abc-123", mode: "new", want: "codex"},
		{name: "copilot neu", vendor: AgentVendorCopilot, mode: "new", want: "copilot --name mgt-navi"},
		{name: "copilot neu mit Run", vendor: AgentVendorCopilot, runID: "abc-123", mode: "new", want: "copilot --name mgt-navi --session-id=abc-123"},
		{name: "copilot resume mit Run", vendor: AgentVendorCopilot, runID: "abc-123", mode: "resume", want: "copilot --name mgt-navi --resume=abc-123"},
		{name: "copilot resume ohne Run", vendor: AgentVendorCopilot, mode: "resume", want: "copilot --name mgt-navi --continue"},
		{name: "gemini neu", vendor: AgentVendorGemini, mode: "new", want: "gemini"},
		{name: "gemini resume", vendor: AgentVendorGemini, runID: "abc-123", mode: "resume", want: "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, ok := providerForVendor(tt.vendor)
			if !ok {
				t.Fatalf("kein Provider für %q", tt.vendor)
			}
			var run *AgentRunRef
			if tt.runID != "" {
				run = &AgentRunRef{Vendor: tt.vendor, ExternalID: tt.runID}
			}
			got, err := provider.StartCommand(session, run, tt.mode)
			if err != nil {
				t.Fatalf("StartCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("StartCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunIDOrigin(t *testing.T) {
	supplied := map[AgentVendor]bool{
		AgentVendorClaude:  true,
		AgentVendorCopilot: true,
		AgentVendorCodex:   false,
		AgentVendorGemini:  false,
	}
	for vendor, want := range supplied {
		provider, ok := providerForVendor(vendor)
		if !ok {
			t.Fatalf("kein Provider für %q", vendor)
		}
		if got := provider.NewRunID() != ""; got != want {
			t.Fatalf("%q liefert vorgegebene Run-ID = %v, want %v", vendor, got, want)
		}
	}
}

func TestCopilotMatchesGithubCopilot(t *testing.T) {
	provider, ok := providerForPaneCommand("github-copilot")
	if !ok || provider.Vendor() != AgentVendorCopilot {
		t.Fatal("github-copilot muss als Copilot erkannt werden")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestVendorStartCommands|TestRunIDOrigin|TestCopilotMatches' -v`
Expected: FAIL, `kein Provider für "codex"`

- [ ] **Step 3: Add the three adapters to `core/provider.go`**

```go
type codexProvider struct{}

func (codexProvider) Vendor() AgentVendor { return AgentVendorCodex }
func (codexProvider) Tool() string        { return AgentToolCodex }
func (codexProvider) Binary() string      { return "codex" }

// Codex assigns its own session id, so the run identity can only be
// discovered from its rollout files after the fact.
func (codexProvider) NewRunID() string { return "" }

func (codexProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "codex")
}

func (codexProvider) StartCommand(_ Session, run *AgentRunRef, mode string) (string, error) {
	if mode == "new" {
		return "codex", nil
	}
	if run != nil && run.ExternalID != "" {
		return "codex resume " + ShellQuote(run.ExternalID), nil
	}
	return "codex resume --last", nil
}

type copilotProvider struct{}

func (copilotProvider) Vendor() AgentVendor { return AgentVendorCopilot }
func (copilotProvider) Tool() string        { return AgentToolCopilot }
func (copilotProvider) Binary() string      { return "copilot" }
func (copilotProvider) NewRunID() string    { return NewUUID() }

func (copilotProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "copilot") || paneCommand == "github-copilot"
}

func (copilotProvider) StartCommand(session Session, run *AgentRunRef, mode string) (string, error) {
	command := "copilot --name " + ShellQuote(session.TmuxName())
	if run != nil && run.ExternalID != "" {
		// Both flags accept the value only in "=" form without ambiguity:
		// --resume takes an optional value and would otherwise swallow the
		// next positional argument.
		flag := "--resume="
		if mode == "new" {
			flag = "--session-id="
		}
		return command + " " + flag + ShellQuote(run.ExternalID), nil
	}
	if mode != "new" {
		command += " --continue"
	}
	return command, nil
}

type geminiProvider struct{}

func (geminiProvider) Vendor() AgentVendor { return AgentVendorGemini }
func (geminiProvider) Tool() string        { return AgentToolGemini }
func (geminiProvider) Binary() string      { return "gemini" }
func (geminiProvider) NewRunID() string    { return "" }

func (geminiProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "gemini")
}

// Gemini CLI has no verified resume form. Starting fresh is the conservative
// contract; the run identity is discovered from ~/.gemini/tmp afterwards.
func (geminiProvider) StartCommand(Session, *AgentRunRef, string) (string, error) {
	return "gemini", nil
}
```

Und die Registrierung erweitern:

```go
func builtinAgentProviders() []AgentProvider {
	return []AgentProvider{claudeProvider{}, codexProvider{}, geminiProvider{}, copilotProvider{}}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./core/ -run 'TestVendorStartCommands|TestRunIDOrigin|TestCopilotMatches|TestDetectAgentTool|TestClaudeProvider|TestProviderForPaneCommand' -v`
Expected: PASS, inklusive der bereits vorhandenen `TestDetectAgentTool`-Tabelle.

- [ ] **Step 5: Commit**

```bash
git add core/provider.go core/provider_test.go
git commit -m "feat: Adapter für Codex, Copilot und Gemini"
```

---

### Task 3: Vendor als durable Session-Eigenschaft

**Files:**
- Modify: `core/state.go:83-105` (Feld `Vendor`), `core/state.go:149-160`
  (Helfer `SessionVendor`)
- Modify: `core/registry.go:531-556` (Migration), `core/registry.go:568+`
  (`validateRegistryState`)
- Modify: `core/lifecycle.go:100-115` (`SessionProvision`),
  `core/lifecycle.go:318-327` (Provisionierung)
- Test: `core/provider_test.go`

**Interfaces:**
- Consumes: `providerForVendor` aus Task 1.
- Produces: `Session.Vendor AgentVendor`, `Session.SessionVendor() AgentVendor`,
  `SessionProvision.Vendor AgentVendor`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSessionVendorDefaultsToClaude(t *testing.T) {
	coding := Session{Name: "navi", SessionKind: SessionKindCodingAgent}
	if got := coding.SessionVendor(); got != AgentVendorClaude {
		t.Fatalf("SessionVendor = %q, want %q", got, AgentVendorClaude)
	}
	stored := Session{Name: "navi", SessionKind: SessionKindCodingAgent, Vendor: AgentVendorCodex}
	if got := stored.SessionVendor(); got != AgentVendorCodex {
		t.Fatalf("SessionVendor = %q, want %q", got, AgentVendorCodex)
	}
	term := Session{Name: "term-navi", Kind: KindTerm}
	if got := term.SessionVendor(); got != "" {
		t.Fatalf("Terminal-SessionVendor = %q, want leer", got)
	}
}

func TestProvisionRecordsVendorAndRun(t *testing.T) {
	tests := []struct {
		name      string
		vendor    AgentVendor
		wantRuns  int
		wantLegacy bool
	}{
		{name: "ohne Angabe wird Claude", vendor: "", wantRuns: 1, wantLegacy: true},
		{name: "Copilot bekommt eine Run-Ref", vendor: AgentVendorCopilot, wantRuns: 1},
		{name: "Codex startet ohne Run-Ref", vendor: AgentVendorCodex, wantRuns: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := provisionedCodingSession(t, tt.vendor)
			wantVendor := tt.vendor
			if wantVendor == "" {
				wantVendor = AgentVendorClaude
			}
			if session.Vendor != wantVendor {
				t.Fatalf("Vendor = %q, want %q", session.Vendor, wantVendor)
			}
			if len(session.AgentRuns) != tt.wantRuns {
				t.Fatalf("AgentRuns = %d, want %d", len(session.AgentRuns), tt.wantRuns)
			}
			if tt.wantRuns == 1 && session.AgentRuns[0].Vendor != wantVendor {
				t.Fatalf("Run-Vendor = %q, want %q", session.AgentRuns[0].Vendor, wantVendor)
			}
			if (session.SessionID != "") != tt.wantLegacy {
				t.Fatalf("Legacy-SessionID gesetzt = %v, want %v", session.SessionID != "", tt.wantLegacy)
			}
		})
	}
}

func TestProvisionRejectsUnknownVendor(t *testing.T) {
	if _, err := newTestLifecycle(t).Provision(context.Background(), SessionProvision{
		Name: "navi", Directory: t.TempDir(), Kind: SessionKindCodingAgent, Vendor: AgentVendor("cursor"),
	}); err == nil {
		t.Fatal("unbekannter Vendor muss abgelehnt werden")
	}
}

func TestRegistryMigrationDefaultsVendor(t *testing.T) {
	state := &State{Agents: []Session{{
		ID: "s1", Name: "navi", RuntimeName: "mgt-navi", Dir: "/work/navi",
		SessionKind: SessionKindCodingAgent, SessionID: "legacy-run",
	}}}
	normalizeRegistryState(state)
	session := state.Agents[0]
	if session.Vendor != AgentVendorClaude {
		t.Fatalf("Vendor nach Migration = %q, want %q", session.Vendor, AgentVendorClaude)
	}
	if run, ok := session.AgentRun(AgentVendorClaude); !ok || run.ExternalID != "legacy-run" {
		t.Fatalf("Legacy-Run ging verloren: %+v", session.AgentRuns)
	}
}
```

Die beiden Helfer `provisionedCodingSession` und `newTestLifecycle` gehören in
dieselbe Testdatei. `newTestLifecycle` muss dem Muster folgen, das die
vorhandenen Lifecycle-Tests bereits verwenden — vor dem Schreiben
`core/lifecycle_test.go` (oder die Datei, die `SessionLifecycle` konstruiert)
lesen und denselben Aufbau übernehmen, inklusive gesetztem `MAGENTIC_STATE`
auf ein `t.TempDir()` und der dort verwendeten Test-Runtime-Attrappe:

```go
func provisionedCodingSession(t *testing.T, vendor AgentVendor) Session {
	t.Helper()
	result, err := newTestLifecycle(t).Provision(context.Background(), SessionProvision{
		Name: "navi", Directory: t.TempDir(), Kind: SessionKindCodingAgent, Vendor: vendor,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return result.Session
}
```

Der Name der Migrationsfunktion in `core/registry.go` ist beim Schreiben aus
der Datei zu übernehmen; im Test oben steht `normalizeRegistryState`
stellvertretend für die Funktion, die den Block bei `core/registry.go:531`
enthält.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestSessionVendor|TestProvision|TestRegistryMigrationDefaultsVendor' -v`
Expected: FAIL, `session.Vendor undefined`

- [ ] **Step 3: Add the field and the helper in `core/state.go`**

Im `Session`-Struct hinter `SessionID` einfügen:

```go
	Vendor           AgentVendor         `json:"vendor,omitempty"`
```

Und hinter `AgentRun`:

```go
// SessionVendor is the durable vendor that starts this Session. An empty
// stored value means Claude, which keeps every pre-multi-provider state
// valid. Terminal Sessions host no coding agent and have no vendor.
func (a Session) SessionVendor() AgentVendor {
	if a.IsTerm() {
		return ""
	}
	if a.Vendor != "" {
		return a.Vendor
	}
	return AgentVendorClaude
}
```

- [ ] **Step 4: Migrate stored state in `core/registry.go`**

In derselben Funktion, die bei `core/registry.go:531` die Legacy-`SessionID`
in eine `AgentRunRef` überführt, direkt vor diesem Block:

```go
	if !session.IsTerm() && session.Vendor == "" {
		session.Vendor = AgentVendorClaude
	}
```

In `validateRegistryState` bei der Session-Schleife ergänzen:

```go
		if !session.IsTerm() && session.Vendor != "" {
			if _, known := providerForVendor(session.Vendor); !known {
				return fmt.Errorf("Session %q hat einen unbekannten Agent-Vendor %q", session.Name, session.Vendor)
			}
		}
```

- [ ] **Step 5: Carry the vendor through provisioning in `core/lifecycle.go`**

`SessionProvision` (bei `core/lifecycle.go:100`) um ein Feld erweitern:

```go
	Vendor           AgentVendor
```

Den `else`-Zweig bei `core/lifecycle.go:324-328` ersetzen:

```go
	} else {
		vendor := request.Vendor
		if vendor == "" {
			vendor = AgentVendorClaude
		}
		provider, known := providerForVendor(vendor)
		if !known {
			return SessionLifecycleResult{}, fmt.Errorf("unbekannter Agent-Vendor %q", vendor)
		}
		session.Vendor = vendor
		if runID := provider.NewRunID(); runID != "" {
			if vendor == AgentVendorClaude {
				// SessionID is the legacy Claude-only run field and stays in
				// step with the canonical AgentRunRef.
				session.SessionID = runID
			}
			session.AgentRuns = []AgentRunRef{{Vendor: vendor, ExternalID: runID}}
		}
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./core/ -run 'TestSessionVendor|TestProvision|TestRegistryMigrationDefaultsVendor' -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add core/state.go core/registry.go core/lifecycle.go core/provider_test.go
git commit -m "feat: Vendor als durable Session-Eigenschaft"
```

---

### Task 4: Start und Resume über den Provider

**Files:**
- Modify: `core/lifecycle.go:1637-1670` (`tmuxLifecycleRuntime.Start`)
- Modify: `core/lifecycle.go:1687-1700` (`DeliverInitial`)
- Test: `core/provider_test.go`

**Interfaces:**
- Consumes: `providerForVendor`, `providerBinaryAvailable`,
  `Session.SessionVendor()` aus Tasks 1–3.
- Produces: `resolveSessionProvider(Session) (AgentProvider, error)` in
  `core/provider.go`.

- [ ] **Step 1: Write the failing test**

```go
func TestResolveSessionProvider(t *testing.T) {
	if _, err := resolveSessionProvider(Session{Name: "navi", SessionKind: SessionKindCodingAgent}); err != nil {
		t.Fatalf("Claude-Standard muss auflösbar sein: %v", err)
	}
	if _, err := resolveSessionProvider(Session{
		Name: "navi", SessionKind: SessionKindCodingAgent, Vendor: AgentVendor("cursor"),
	}); err == nil {
		t.Fatal("unbekannter Vendor muss einen Fehler liefern")
	}
	if _, err := resolveSessionProvider(Session{Name: "term-navi", Kind: KindTerm}); err == nil {
		t.Fatal("eine Terminal-Session hat keinen Provider")
	}
}

func TestStartCommandForSession(t *testing.T) {
	session := Session{
		Name: "navi", RuntimeName: "mgt-navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorCodex,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorCodex, ExternalID: "abc-123"}},
	}
	got, err := startCommandForSession(session, "resume")
	if err != nil {
		t.Fatalf("startCommandForSession: %v", err)
	}
	if got != "codex resume abc-123" {
		t.Fatalf("startCommandForSession = %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./core/ -run 'TestResolveSessionProvider|TestStartCommandForSession' -v`
Expected: FAIL, `undefined: resolveSessionProvider`

- [ ] **Step 3: Add both helpers to `core/provider.go`**

```go
// resolveSessionProvider is the single place that turns a Session into the
// Adapter that starts it. A terminal Session has no coding agent and is a
// caller error here, not a silent no-op.
func resolveSessionProvider(session Session) (AgentProvider, error) {
	vendor := session.SessionVendor()
	if vendor == "" {
		return nil, fmt.Errorf("Session %q hostet keinen Coding-Agenten", session.Name)
	}
	provider, known := providerForVendor(vendor)
	if !known {
		return nil, fmt.Errorf("Session %q hat einen unbekannten Agent-Vendor %q", session.Name, vendor)
	}
	return provider, nil
}

// startCommandForSession resolves the Session's own AgentRunRef and asks its
// provider for the command line.
func startCommandForSession(session Session, mode string) (string, error) {
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return "", err
	}
	var runRef *AgentRunRef
	if run, ok := session.AgentRun(provider.Vendor()); ok {
		runRef = &run
	}
	return provider.StartCommand(session, runRef, mode)
}
```

`"fmt"` in den Import-Block von `core/provider.go` aufnehmen.

- [ ] **Step 4: Rewrite `tmuxLifecycleRuntime.Start`**

`core/lifecycle.go:1637-1670` vollständig ersetzen. Die Binary-Prüfung steht
**vor** `tmux new-session`, damit ein fehlendes Programm gar keine tmux-Session
entstehen lässt:

```go
func (tmuxLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Session directory %q is unavailable", session.Dir)
	}
	var provider AgentProvider
	if !session.IsTerm() {
		resolved, err := resolveSessionProvider(session)
		if err != nil {
			return err
		}
		if !providerBinaryAvailable(resolved) {
			return fmt.Errorf("%s ist nicht installiert (%s nicht im PATH)", resolved.Vendor(), resolved.Binary())
		}
		provider = resolved
	}
	args := []string{"new-session", "-d", "-s", session.TmuxName(), "-c", session.Dir, "-x", "220", "-y", "50"}
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	TmuxConfigureUX()
	if session.IsTerm() {
		return nil
	}
	var runRef *AgentRunRef
	if run, ok := session.AgentRun(provider.Vendor()); ok {
		runRef = &run
	}
	command, err := provider.StartCommand(session, runRef, mode)
	if err != nil {
		return err
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "-l", command).CombinedOutput(); err != nil {
		return fmt.Errorf("start coding agent: %w", err)
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("submit coding-agent command: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Use the provider's tool in `DeliverInitial`**

In `core/lifecycle.go:1687-1700` die Konstante `AgentToolClaude` ersetzen:

```go
func (tmuxLifecycleRuntime) DeliverInitial(_ context.Context, session Session, prompt string) (bool, error) {
	if session.IsTerm() {
		return false, errors.New("initial coding prompt cannot be delivered to a terminal Session")
	}
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return false, err
	}
	// enqueuePrompt confirms only in-process scheduling. The durable state
	// therefore remains delivery_unknown until a future observation can prove
	// acceptance; reconciliation intentionally does not submit it again.
	if err := enqueuePrompt(session.TmuxName(), prompt, true, provider.Tool(), true, true, false, nil); err != nil {
		return false, err
	}
	return false, nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./core/ -run 'TestResolveSessionProvider|TestStartCommandForSession|TestProvision' -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add core/provider.go core/lifecycle.go core/provider_test.go
git commit -m "feat: Session-Start über den Provider-Adapter"
```

---

### Task 5: Run-Identität aus dem Vendor-Verlauf ermitteln

Codex und Gemini vergeben ihre Run-ID selbst. Diese Aufgabe ermittelt sie
träge: erst wenn eine Fortsetzung ansteht und keine `AgentRunRef` vorliegt.

**Files:**
- Create: `core/provider_run.go`
- Modify: `core/registry.go:44-60` (neue Änderungsart),
  `core/registry.go:101-165` (Konstruktor), `core/registry.go:333-385` (Anwendung)
- Modify: `core/lifecycle.go:1287-1292` (Auflösung vor dem Start)
- Test: `core/provider_run_test.go`

**Interfaces:**
- Consumes: `OpenWorkHistory`, `WorkHistoryConfig`, `NewHistoryAssociations`,
  `HistoryEventQuery`, `HistoryEvent`, `historyProviderFromAgentVendor` aus
  `core/workhistory.go`; `resolveSessionProvider` aus Task 4.
- Produces: `RecordAgentRun(sessionID SessionID, name string, run AgentRunRef) RegistryChange`,
  `discoverAgentRun(ctx context.Context, session Session, events []HistoryEvent) (AgentRunRef, bool)`,
  `resolveMissingAgentRun(ctx context.Context, session Session) (Session, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestDiscoverAgentRun(t *testing.T) {
	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	session := Session{
		Name: "navi", Dir: "/work/navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorCodex, CreatedAt: created,
	}
	event := func(id, cwd string, at time.Time) HistoryEvent {
		return HistoryEvent{
			Provider:       HistoryProviderCodex,
			ConversationID: historyKnown(id),
			CWD:            historyKnown(cwd),
			OccurredAt:     historyKnown(at),
		}
	}
	tests := []struct {
		name   string
		events []HistoryEvent
		want   string
		found  bool
	}{
		{
			name:   "genau ein Lauf im richtigen Verzeichnis",
			events: []HistoryEvent{event("run-1", "/work/navi", created.Add(time.Minute))},
			want:   "run-1", found: true,
		},
		{
			name:   "fremdes Verzeichnis zählt nicht",
			events: []HistoryEvent{event("run-1", "/work/other", created.Add(time.Minute))},
			found:  false,
		},
		{
			name:   "Lauf vor der Session zählt nicht",
			events: []HistoryEvent{event("run-1", "/work/navi", created.Add(-time.Minute))},
			found:  false,
		},
		{
			name: "mehrdeutig bleibt ohne Ergebnis",
			events: []HistoryEvent{
				event("run-1", "/work/navi", created.Add(time.Minute)),
				event("run-2", "/work/navi", created.Add(2*time.Minute)),
			},
			found: false,
		},
		{
			name:   "derselbe Lauf mehrfach ist eindeutig",
			events: []HistoryEvent{
				event("run-1", "/work/navi", created.Add(time.Minute)),
				event("run-1", "/work/navi", created.Add(2*time.Minute)),
			},
			want: "run-1", found: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, ok := discoverAgentRun(context.Background(), session, tt.events)
			if ok != tt.found {
				t.Fatalf("gefunden = %v, want %v", ok, tt.found)
			}
			if ok && (run.ExternalID != tt.want || run.Vendor != AgentVendorCodex) {
				t.Fatalf("Run = %+v, want %q/%q", run, AgentVendorCodex, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./core/ -run TestDiscoverAgentRun -v`
Expected: FAIL, `undefined: discoverAgentRun`

- [ ] **Step 3: Create `core/provider_run.go`**

```go
package core

import (
	"context"
	"path/filepath"
	"time"
)

// discoverAgentRun resolves the run identity of a Session whose vendor assigns
// its own. Exactness beats coverage: a directory match plus a start after the
// Session was created must name exactly one conversation, otherwise the
// Session deliberately stays without a run reference.
func discoverAgentRun(_ context.Context, session Session, events []HistoryEvent) (AgentRunRef, bool) {
	vendor := session.SessionVendor()
	provider, known := historyProviderFromAgentVendor(vendor)
	if !known || session.Dir == "" || session.CreatedAt.IsZero() {
		return AgentRunRef{}, false
	}
	dir := filepath.Clean(session.Dir)
	candidate := ""
	for _, event := range events {
		if event.Provider != provider || !event.ConversationID.Known() || !event.CWD.Known() {
			continue
		}
		if filepath.Clean(event.CWD.Value) != dir {
			continue
		}
		if !event.OccurredAt.Known() || event.OccurredAt.Value.Before(session.CreatedAt) {
			continue
		}
		switch {
		case candidate == "":
			candidate = event.ConversationID.Value
		case candidate != event.ConversationID.Value:
			return AgentRunRef{}, false
		}
	}
	if candidate == "" {
		return AgentRunRef{}, false
	}
	return AgentRunRef{Vendor: vendor, ExternalID: candidate}, true
}

// resolveMissingAgentRun reads the vendor's history once and persists a found
// run reference. It returns the Session unchanged when nothing is resolvable;
// a missing reference is a normal outcome, not an error.
func resolveMissingAgentRun(ctx context.Context, session Session) (Session, error) {
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return session, err
	}
	if provider.NewRunID() != "" {
		return session, nil
	}
	if _, exists := session.AgentRun(provider.Vendor()); exists {
		return session, nil
	}
	historyProvider, known := historyProviderFromAgentVendor(provider.Vendor())
	if !known {
		return session, nil
	}
	history, err := OpenWorkHistory(WorkHistoryConfig{})
	if err != nil {
		return session, nil
	}
	state, err := LoadState()
	if err != nil {
		return session, nil
	}
	page, err := history.Events(ctx, NewHistoryAssociations(*state), HistoryEventQuery{
		Providers: []HistoryProvider{historyProvider},
		Since:     session.CreatedAt,
	})
	if err != nil {
		return session, nil
	}
	run, found := discoverAgentRun(ctx, session, page.Events)
	if !found {
		return session, nil
	}
	result, err := OpenRegistry(StatePath()).Change(ctx, RecordAgentRun(session.ID, session.Name, run))
	if err != nil || !result.Applied {
		return session, nil
	}
	updated := result.Snapshot.State()
	if resolved := updated.SessionByID(session.ID); resolved != nil {
		return *resolved, nil
	}
	return session, nil
}
```

Die Zeitgrenze `Since` liegt bewusst auf `session.CreatedAt`; `discoverAgentRun`
prüft sie noch einmal selbst, damit die Regel auch für Aufrufer mit anderer
Ereignisquelle gilt.

- [ ] **Step 4: Add the registry change**

In `core/registry.go` bei den Änderungsarten (`core/registry.go:44-60`)
ergänzen:

```go
	registryRecordAgentRun
```

Im `RegistryChange`-Struct ein Feld ergänzen:

```go
	agentRun    AgentRunRef
```

Konstruktor neben `RenameRegisteredSessionRuntime`:

```go
// RecordAgentRun stores a vendor-qualified run reference that was discovered
// from the vendor's own history. An existing reference for that vendor is
// never overwritten: run identity is durable once known.
func RecordAgentRun(sessionID SessionID, name string, run AgentRunRef) RegistryChange {
	return RegistryChange{kind: registryRecordAgentRun, sessionID: sessionID, sessionName: name, agentRun: run}
}
```

In der Anwendung die Fallliste bei `core/registry.go:333` um
`registryRecordAgentRun` erweitern und im inneren `switch` ergänzen:

```go
		case registryRecordAgentRun:
			if change.agentRun.Vendor == "" || change.agentRun.ExternalID == "" {
				return false, "", "", fmt.Errorf("unvollständige AgentRunRef für Session %q", session.Name)
			}
			if _, exists := session.AgentRun(change.agentRun.Vendor); exists {
				return false, "", session.ID, nil
			}
			session.AgentRuns = append(session.AgentRuns, change.agentRun)
```

- [ ] **Step 5: Resolve before a resume in `core/lifecycle.go`**

Direkt vor `startErr := l.runtime.Start(...)` bei `core/lifecycle.go:1292`:

```go
		if !record.Session.IsTerm() && record.StartMode != "new" {
			// A vendor that assigns its own run id can only be resumed once
			// its identity was found. A failure here is not fatal: the
			// provider falls back to its own continuation form.
			if resolved, resolveErr := resolveMissingAgentRun(ctx, record.Session); resolveErr == nil {
				record.Session = resolved
			}
		}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./core/ -run 'TestDiscoverAgentRun|TestProvision' -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add core/provider_run.go core/provider_run_test.go core/registry.go core/lifecycle.go
git commit -m "feat: Run-Identität aus dem Vendor-Verlauf ermitteln"
```

---

### Task 6: Vendor einer Session wechseln

**Files:**
- Modify: `core/registry.go` (Änderungsart `registrySetVendor`)
- Modify: `core/lifecycle.go` (Operation `SwitchVendor` am `SessionLifecycle`)
- Modify: `core/actions.go` (öffentliche Aktion)
- Test: `core/provider_switch_test.go`

**Interfaces:**
- Consumes: `providerForVendor`, `providerBinaryAvailable` (Task 1),
  `Session.SessionVendor()` (Task 3), `l.withSessionTransition`,
  `l.runtime.Stop`, `l.runtime.Start` aus `core/lifecycle.go`.
- Produces: `SetSessionVendor(sessionID SessionID, name string, vendor AgentVendor) RegistryChange`,
  `(*SessionLifecycle).SwitchVendor(ctx context.Context, sessionID SessionID, vendor AgentVendor) (Session, error)`,
  `SwitchSessionVendor(sessionID SessionID, vendor string) error`.

- [ ] **Step 1: Write the failing test**

```go
func TestSwitchVendorRestartsAndKeepsRuns(t *testing.T) {
	lifecycle, runtime := newRecordingLifecycle(t)
	session := provisionedCodingSession(t, AgentVendorClaude)
	runtime.reset()

	switched, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorCodex)
	if err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if switched.Vendor != AgentVendorCodex {
		t.Fatalf("Vendor = %q, want %q", switched.Vendor, AgentVendorCodex)
	}
	if _, ok := switched.AgentRun(AgentVendorClaude); !ok {
		t.Fatal("die Claude-Run-Ref muss erhalten bleiben")
	}
	if runtime.stops != 1 || runtime.starts != 1 {
		t.Fatalf("Stop/Start = %d/%d, want 1/1", runtime.stops, runtime.starts)
	}
	if runtime.lastStartMode != "new" {
		t.Fatalf("StartMode = %q, want \"new\"", runtime.lastStartMode)
	}
}

func TestSwitchVendorToKnownRunResumes(t *testing.T) {
	lifecycle, runtime := newRecordingLifecycle(t)
	session := provisionedCodingSession(t, AgentVendorClaude)
	if _, err := OpenRegistry(StatePath()).Change(context.Background(), RecordAgentRun(
		session.ID, session.Name, AgentRunRef{Vendor: AgentVendorCodex, ExternalID: "run-9"},
	)); err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorCodex); err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if runtime.lastStartMode != "resume" {
		t.Fatalf("StartMode = %q, want \"resume\"", runtime.lastStartMode)
	}
}

func TestSwitchVendorToSameVendorIsNoop(t *testing.T) {
	lifecycle, runtime := newRecordingLifecycle(t)
	session := provisionedCodingSession(t, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorClaude); err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if runtime.stops != 0 || runtime.starts != 0 {
		t.Fatalf("Stop/Start = %d/%d, want 0/0", runtime.stops, runtime.starts)
	}
}

func TestSwitchVendorRejectsTerminal(t *testing.T) {
	lifecycle, _ := newRecordingLifecycle(t)
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		Name: "term-navi", Directory: t.TempDir(), Kind: SessionKindTerminal,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, err := lifecycle.SwitchVendor(context.Background(), result.Session.ID, AgentVendorCodex); err == nil {
		t.Fatal("eine Terminal-Session hat keinen Vendor")
	}
}

func TestSwitchVendorRejectsUnknownVendor(t *testing.T) {
	lifecycle, runtime := newRecordingLifecycle(t)
	session := provisionedCodingSession(t, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendor("cursor")); err == nil {
		t.Fatal("unbekannter Vendor muss abgelehnt werden")
	}
	if runtime.stops != 0 {
		t.Fatal("bei einem abgelehnten Wechsel darf nichts beendet werden")
	}
}
```

`newRecordingLifecycle` baut denselben `SessionLifecycle` wie
`newTestLifecycle` aus Task 3, mit einer Runtime-Attrappe, die `stops`,
`starts` und `lastStartMode` zählt und `reset()` anbietet. Vor dem Schreiben
die vorhandene Lifecycle-Testattrappe lesen und deren Struktur übernehmen,
statt eine zweite Bauform einzuführen.

Die Binary-Prüfung wird in diesen Tests nicht ausgelöst, weil die
Runtime-Attrappe `Start` ersetzt. Der fail-closed Pfad des Wechsels bekommt
deshalb einen eigenen Test, sobald ein Vendor ohne Binary geprüft werden kann:

```go
func TestSwitchVendorRequiresBinary(t *testing.T) {
	provider, ok := providerForVendor(AgentVendorGemini)
	if !ok {
		t.Fatal("kein Gemini-Provider")
	}
	if providerBinaryAvailable(provider) {
		t.Skip("gemini ist auf dieser Maschine installiert")
	}
	lifecycle, runtime := newRecordingLifecycle(t)
	session := provisionedCodingSession(t, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorGemini); err == nil {
		t.Fatal("ein Vendor ohne Binary darf nicht übernommen werden")
	}
	if runtime.stops != 0 {
		t.Fatal("die laufende Session muss unberührt bleiben")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run TestSwitchVendor -v`
Expected: FAIL, `lifecycle.SwitchVendor undefined`

- [ ] **Step 3: Add the registry change**

In `core/registry.go` analog zu Task 5: Änderungsart `registrySetVendor`,
Feld `vendor AgentVendor` am `RegistryChange`, Konstruktor:

```go
// SetSessionVendor records which coding-agent vendor starts this Session from
// now on. AgentRuns of other vendors stay untouched, so a Session can carry a
// run reference per vendor and be switched back without losing history.
func SetSessionVendor(sessionID SessionID, name string, vendor AgentVendor) RegistryChange {
	return RegistryChange{kind: registrySetVendor, sessionID: sessionID, sessionName: name, vendor: vendor}
}
```

Anwendung im inneren `switch`:

```go
		case registrySetVendor:
			if session.IsTerm() {
				return false, "", "", fmt.Errorf("Session %q ist ein Terminal und hat keinen Agent-Vendor", session.Name)
			}
			if _, known := providerForVendor(change.vendor); !known {
				return false, "", "", fmt.Errorf("unbekannter Agent-Vendor %q", change.vendor)
			}
			if session.Vendor == change.vendor {
				return false, "", session.ID, nil
			}
			session.Vendor = change.vendor
```

- [ ] **Step 4: Add `SwitchVendor` to `core/lifecycle.go`**

Neben den vorhandenen Lifecycle-Operationen:

```go
// SwitchVendor moves a running Session to another coding-agent vendor. The
// target's binary is checked before anything is stopped, so a missing program
// leaves the running Session untouched. AgentRuns of every vendor survive the
// switch; the target resumes when it already has one.
func (l *SessionLifecycle) SwitchVendor(ctx context.Context, sessionID SessionID, vendor AgentVendor) (Session, error) {
	provider, known := providerForVendor(vendor)
	if !known {
		return Session{}, fmt.Errorf("unbekannter Agent-Vendor %q", vendor)
	}
	if !providerBinaryAvailable(provider) {
		return Session{}, fmt.Errorf("%s ist nicht installiert (%s nicht im PATH)", vendor, provider.Binary())
	}
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return Session{}, err
	}
	state := snapshot.State()
	session := state.SessionByID(sessionID)
	if session == nil {
		return Session{}, fmt.Errorf("Session %q nicht gefunden", sessionID)
	}
	if session.IsTerm() {
		return Session{}, fmt.Errorf("Session %q ist ein Terminal und hat keinen Agent-Vendor", session.Name)
	}
	current := *session
	if current.SessionVendor() == vendor {
		return current, nil
	}
	var switched Session
	err = l.withSessionTransition(ctx, current.ID, current.Name, func() error {
		fresh, err := l.registry.Snapshot(ctx)
		if err != nil {
			return err
		}
		freshState := fresh.State()
		resolved := freshState.SessionByID(current.ID)
		if resolved == nil {
			return fmt.Errorf("Session %q wurde während des Wechsels entfernt", current.Name)
		}
		if err := l.runtime.Stop(ctx, *resolved); err != nil {
			return err
		}
		result, err := l.registry.Change(ctx, SetSessionVendor(resolved.ID, resolved.Name, vendor))
		if err != nil {
			return err
		}
		updated := result.Snapshot.State()
		target := updated.SessionByID(resolved.ID)
		if target == nil {
			return fmt.Errorf("Session %q wurde während des Wechsels entfernt", resolved.Name)
		}
		mode := "new"
		if _, hasRun := target.AgentRun(vendor); hasRun {
			mode = "resume"
		}
		if err := l.runtime.Start(ctx, *target, mode); err != nil {
			return err
		}
		switched = *target
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return switched, nil
}
```

Trifft `l.runtime.Stop` auf eine bereits beendete Runtime, ist das kein
Fehlerfall der Operation: `tmuxLifecycleRuntime.Stop` meldet dann einen
tmux-Fehler. Deshalb vor dem Stop prüfen und ihn nur bei vorhandener Runtime
ausführen:

```go
		exists, err := l.runtime.Exists(ctx, *resolved)
		if err != nil {
			return err
		}
		if exists {
			if err := l.runtime.Stop(ctx, *resolved); err != nil {
				return err
			}
		}
```

Dieser Block ersetzt den unbedingten `l.runtime.Stop`-Aufruf oben. Der Test
`TestSwitchVendorRestartsAndKeepsRuns` erwartet `stops == 1`, weil die
Attrappe `Exists` mit `true` beantwortet; die Attrappe muss das entsprechend
tun.

- [ ] **Step 5: Add the public action in `core/actions.go`**

Neben den übrigen ID-basierten Aktionen:

```go
// SwitchSessionVendor changes which coding agent a Session runs.
func SwitchSessionVendor(sessionID SessionID, vendor string) error {
	_, err := defaultSessionLifecycle().SwitchVendor(context.Background(), sessionID, AgentVendor(strings.TrimSpace(vendor)))
	return err
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./core/ -run TestSwitchVendor -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add core/registry.go core/lifecycle.go core/actions.go core/provider_switch_test.go
git commit -m "feat: Vendor einer laufenden Session wechseln"
```

---

### Task 7: Vendor in Overview, Bindings und Oberfläche

**Files:**
- Modify: `core/overview.go:10-32` (`OvAgent`), `core/overview.go:500-536` (`toOvAgent`)
- Create: `core/provider_catalog.go` (Vendor-Katalog für die UI)
- Modify: `app/app.go:417-423` (`NewSession`), plus zwei neue Bindungen
- Modify: `app/frontend/src/main.js` (Vendor-Auswahl und Wechsel)
- Test: `core/provider_catalog_test.go`, `core/overview_test.go`

**Interfaces:**
- Consumes: `builtinAgentProviders`, `providerBinaryAvailable` (Task 1),
  `Session.SessionVendor()` (Task 3), `SwitchSessionVendor` (Task 6).
- Produces: `OvAgent.Vendor string`, `AgentVendorOption` mit den Feldern
  `Vendor string`, `Label string`, `Available bool`,
  `AgentVendorCatalog() []AgentVendorOption`,
  `(*App) AgentVendors() []core.AgentVendorOption`,
  `(*App) NewSessionWithVendor(projectID string, worktree bool, name, vendor string) (string, error)`,
  `(*App) SwitchSessionVendor(sessionID, vendor string) error`.

- [ ] **Step 1: Write the failing tests**

```go
func TestAgentVendorCatalog(t *testing.T) {
	catalog := AgentVendorCatalog()
	if len(catalog) != 4 {
		t.Fatalf("Katalog hat %d Einträge, want 4", len(catalog))
	}
	want := map[string]string{
		string(AgentVendorClaude):  "Claude Code",
		string(AgentVendorCodex):   "Codex",
		string(AgentVendorGemini):  "Gemini CLI",
		string(AgentVendorCopilot): "GitHub Copilot",
	}
	for _, option := range catalog {
		label, known := want[option.Vendor]
		if !known {
			t.Fatalf("unerwarteter Vendor %q", option.Vendor)
		}
		if option.Label != label {
			t.Fatalf("Label für %q = %q, want %q", option.Vendor, option.Label, label)
		}
		delete(want, option.Vendor)
	}
	if len(want) != 0 {
		t.Fatalf("fehlende Vendors: %v", want)
	}
	if catalog[0].Vendor != string(AgentVendorClaude) {
		t.Fatalf("Claude muss zuerst stehen, ist aber %q", catalog[0].Vendor)
	}
}

func TestOverviewCarriesSessionVendor(t *testing.T) {
	agent := Session{
		ID: "s1", Name: "navi", Dir: "/work/navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorCodex, CreatedAt: time.Now(),
	}
	ov := toOvAgent(agent, SessionObservation{Status: StatusIdle, Tool: AgentToolCodex}, "main")
	if ov.Vendor != string(AgentVendorCodex) {
		t.Fatalf("OvAgent.Vendor = %q, want %q", ov.Vendor, AgentVendorCodex)
	}
	term := Session{ID: "s2", Name: "term-navi", Kind: KindTerm, CreatedAt: time.Now()}
	if got := toOvAgent(term, SessionObservation{Status: StatusTerm, Tool: AgentToolBash}, "main").Vendor; got != "" {
		t.Fatalf("Terminal-Vendor = %q, want leer", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestAgentVendorCatalog|TestOverviewCarriesSessionVendor' -v`
Expected: FAIL, `undefined: AgentVendorCatalog`

- [ ] **Step 3: Create `core/provider_catalog.go`**

```go
package core

// AgentVendorOption is what the UI needs to offer one vendor: its durable
// value, a human label, and whether its program is installed. A vendor whose
// binary is missing stays visible but unselectable, so the reason a Session
// cannot start is stated before it is attempted.
type AgentVendorOption struct {
	Vendor    string `json:"vendor"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

var agentVendorLabels = map[AgentVendor]string{
	AgentVendorClaude:  "Claude Code",
	AgentVendorCodex:   "Codex",
	AgentVendorGemini:  "Gemini CLI",
	AgentVendorCopilot: "GitHub Copilot",
}

// AgentVendorCatalog lists the selectable vendors in presentation order.
func AgentVendorCatalog() []AgentVendorOption {
	providers := builtinAgentProviders()
	catalog := make([]AgentVendorOption, 0, len(providers))
	for _, provider := range providers {
		label := agentVendorLabels[provider.Vendor()]
		if label == "" {
			label = string(provider.Vendor())
		}
		catalog = append(catalog, AgentVendorOption{
			Vendor:    string(provider.Vendor()),
			Label:     label,
			Available: providerBinaryAvailable(provider),
		})
	}
	return catalog
}
```

Die Reihenfolge folgt `builtinAgentProviders()`; Claude steht dort zuerst.

- [ ] **Step 4: Carry the vendor into the Overview**

In `OvAgent` hinter `Tool`:

```go
	Vendor        string    `json:"vendor,omitempty"`
```

In `toOvAgent` hinter `Tool: tool,`:

```go
		Vendor:        string(a.SessionVendor()),
```

- [ ] **Step 5: Add the Wails bindings in `app/app.go`**

`NewSession` unverändert lassen und daneben ergänzen:

```go
func (a *App) NewSessionWithVendor(projectID string, worktree bool, name, vendor string) (string, error) {
	st, project, err := loadProjectByID(projectID)
	if err != nil {
		return "", err
	}
	return core.CreateAgentSessionWithVendor(st, project.ID, worktree, name, vendor)
}

func (a *App) AgentVendors() []core.AgentVendorOption {
	return core.AgentVendorCatalog()
}

func (a *App) SwitchSessionVendor(sessionID, vendor string) error {
	return core.SwitchSessionVendor(core.SessionID(sessionID), vendor)
}
```

Dazu in `core/actions.go` bei `CreateAgentSession` (Zeile 916) den Vendor
durchreichen, ohne die vorhandene Signatur zu brechen:

```go
func CreateAgentSession(st *State, projectID ProjectID, worktree bool, name string) (string, error) {
	return CreateAgentSessionWithVendor(st, projectID, worktree, name, "")
}

func CreateAgentSessionWithVendor(st *State, projectID ProjectID, worktree bool, name, vendor string) (string, error) {
	// bisheriger Rumpf von CreateAgentSession, mit
	// Vendor: AgentVendor(strings.TrimSpace(vendor)) im SessionProvision
}
```

- [ ] **Step 6: Regenerate the bindings and build**

Run: `cd app && wails generate module && go build ./...`
Expected: `app/frontend/wailsjs` enthält `NewSessionWithVendor`,
`AgentVendors` und `SwitchSessionVendor`; der Build ist grün.

- [ ] **Step 7: Vendor-Auswahl beim Anlegen im Frontend**

In `app/frontend/src/main.js` den Katalog einmal beim Start laden und in einem
Modulzustand halten:

```js
let vendorCatalog = [{ vendor: 'claude', label: 'Claude Code', available: true }];
async function loadVendorCatalog() {
  try {
    const catalog = await AgentVendors();
    if (Array.isArray(catalog) && catalog.length) vendorCatalog = catalog;
  } catch { /* Standardkatalog bleibt */ }
}
```

`loadVendorCatalog()` an derselben Stelle aufrufen, an der die übrigen
Startdaten geladen werden.

Der Plus-Knopf der Projektkarte (`app/frontend/src/main.js:1157-1176`) öffnet
statt des sofortigen Starts das vorhandene Menü mit einem Eintrag je Vendor.
Die Modifikatortasten bleiben erhalten: `⌥` weiterhin Worktree, `⇧`
weiterhin reines Terminal ohne Vendor-Frage.

```js
plus.title = 'Neue Session in ' + p.name + ' (⌥-Klick: in frischem Worktree · ⇧-Klick: reines Terminal)';
plus.onclick = async e => {
  e.stopPropagation();
  if (e.shiftKey) {
    try {
      const name = await act(NewTermSession(p.id, false, ''), n => `Terminal „${n}" geöffnet`);
      if (name) openSessionByName(name);
    } catch { /* toast zeigt den Fehler */ }
    return;
  }
  showVendorMenu(e.clientX, e.clientY, p, e.altKey);
};
```

```js
function showVendorMenu(x, y, project, worktree) {
  menuFor = { project, worktree };
  menuEl.innerHTML =
    `<div class="mi-head">Neue Session in ${esc(project.name)}</div>` +
    vendorCatalog.map(option => option.available
      ? `<div class="mi" data-mi="newvendor" data-vendor="${esc(option.vendor)}">${developerIcon(option.vendor)} ${esc(option.label)}</div>`
      : `<div class="mi disabled" title="${esc(option.label)} ist nicht installiert">${developerIcon(option.vendor)} ${esc(option.label)}</div>`
    ).join('');
  menuEl.style.display = 'block';
  menuEl.style.left = Math.min(x, window.innerWidth - 220) + 'px';
  menuEl.style.top = Math.min(y, window.innerHeight - menuEl.offsetHeight - 10) + 'px';
}
```

Im Klick-Handler von `menuEl` einen Fall ergänzen:

```js
    case 'newvendor': {
      hideMenu();
      const { project, worktree } = menuFor || {};
      if (!project) break;
      try {
        const name = await act(
          NewSessionWithVendor(project.id, !!worktree, '', mi.dataset.vendor),
          n => (worktree ? `Worktree-Session „${n}" gestartet` : `Session „${n}" gestartet`),
        );
        if (!name) break;
        if (view === 'hydra' && hydraProject === project.name) await focusHydraSession(name);
        else openSessionByName(name);
      } catch { /* toast zeigt den Fehler */ }
      break;
    }
```

Da `menuFor` jetzt zwei Formen trägt, prüft der bestehende Handler-Kopf
`const { id, name } = menuFor;` ins Leere, sobald ein Vendor-Menü offen ist.
Deshalb den Kopf auf `const { id, name, project } = menuFor;` erweitern und die
sessionbezogenen Fälle mit `if (!id) break;` absichern.

Die Klasse `.mi.disabled` in `app/frontend/src/style.css` ergänzen, sofern sie
dort noch nicht existiert:

```css
#menu .mi.disabled { opacity: .45; cursor: default; }
```

- [ ] **Step 8: Vendor-Wechsel im Session-Menü**

In `showMenu` (`app/frontend/src/main.js:2227`) im Nicht-`later`-Zweig hinter
dem `done`-Eintrag einfügen:

```js
    const switchable = session && !session.term
      ? vendorCatalog
          .filter(option => option.available && option.vendor !== session.vendor)
          .map(option => `<div class="mi" data-mi="switchvendor" data-vendor="${esc(option.vendor)}">${developerIcon(option.vendor)} Zu ${esc(option.label)} wechseln</div>`)
          .join('')
      : '';
```

und `switchable` in den `innerHTML`-Aufbau aufnehmen. Im Klick-Handler:

```js
    case 'switchvendor':
      if (mi.dataset.confirm) {
        hideMenu();
        try {
          await act(SwitchSessionVendor(id, mi.dataset.vendor), `„${name}" läuft jetzt mit einem anderen Agenten`);
        } catch { /* toast zeigt den Fehler */ }
        await refresh(true);
      } else {
        mi.dataset.confirm = '1';
        mi.innerHTML = icon('play') + ' wirklich wechseln? Der laufende Prozess wird beendet';
      }
      break;
```

Die zweistufige Bestätigung folgt dem vorhandenen `kill`-Muster im selben
Handler.

- [ ] **Step 9: Run the tests and the build**

Run: `go test ./core/ -run 'TestAgentVendorCatalog|TestOverviewCarriesSessionVendor|TestOverview' -v`
Expected: PASS

Run: `cd app/frontend && npm test`
Expected: PASS — insbesondere `session-tool.test.js`, das `session.provider`
bereits abdeckt.

Run: `cd app && go build ./...`
Expected: grün.

- [ ] **Step 10: Commit**

```bash
git add core/overview.go core/provider_catalog.go core/provider_catalog_test.go core/overview_test.go core/actions.go app/app.go app/frontend/src/main.js app/frontend/src/style.css app/frontend/wailsjs
git commit -m "feat: Vendor-Auswahl und Vendor-Wechsel in der Oberfläche"
```

---

## Abschluss

Nach Task 7 ist der Schnitt vollständig. Vor dem Abschluss einmal
`go test ./core/... ./app/...` laufen lassen — aber nur auf ausdrückliche
Aufforderung, wie es die globale Vorgabe verlangt.

Zwei Punkte bleiben bewusst offen und gehören in die Folgeprojekte:

- Codex, Gemini und Copilot melden weiterhin `StatusUnknown`, weil
  `statusForAgentRuntime` unverändert bleibt. Das ist Teilprojekt 2.
- Handoff bleibt zielseitig Claude-only, und Usage rechnet weiter nur mit
  Claude-Preisen. Das ist Teilprojekt 3.
