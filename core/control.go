package core

import (
	"encoding/json"
	"errors"
	"strings"
)

// ControlVerb is the one vocabulary behind both front doors: the CLI marshals
// its arguments into these requests and the socket server dispatches them, so a
// verb cannot exist on one surface only.
type ControlVerb string

const (
	ControlSessionList   ControlVerb = "session.list"
	ControlSessionStart  ControlVerb = "session.start"
	ControlSessionSend   ControlVerb = "session.send"
	ControlSessionOutput ControlVerb = "session.output"
	ControlSessionWait   ControlVerb = "session.wait"
	ControlSessionKill   ControlVerb = "session.kill"
	// ControlSessionWhoami answers the caller's own identity from the
	// environment marker facts it presents.
	ControlSessionWhoami ControlVerb = "session.whoami"
	// ControlSessionWatch turns the connection into an event stream.
	ControlSessionWatch ControlVerb = "session.watch"
)

// ControlVerbs is the complete verb set, in the order the help text lists them.
func ControlVerbs() []ControlVerb {
	specs := ControlVerbSpecs()
	verbs := make([]ControlVerb, 0, len(specs))
	for _, spec := range specs {
		verbs = append(verbs, spec.Verb)
	}
	return verbs
}

// ControlFlagKind is the flag.FlagSet primitive a ControlFlag parses as.
type ControlFlagKind int

const (
	ControlFlagString ControlFlagKind = iota
	ControlFlagBool
	ControlFlagInt
)

// ControlFlag is one flag of one verb's command line, declared once and read
// by both the CLI's flag.FlagSet and its generated help and default checks.
type ControlFlag struct {
	Name    string
	Usage   string
	Kind    ControlFlagKind
	Default string
	// SetString, SetBool and SetInt fill ControlArgs from the parsed flag
	// value; exactly the one matching Kind is ever called.
	SetString func(*ControlArgs, string)
	SetBool   func(*ControlArgs, bool)
	SetInt    func(*ControlArgs, int)
}

// ControlVerbSpec is the single declaration behind one verb's CLI flags, help
// line and dispatch. A new verb needs one new entry here, not six edits.
type ControlVerbSpec struct {
	Verb    ControlVerb
	Summary string
	Flags   []ControlFlag
	// After runs once flags are parsed, for the handful of things a flag
	// alone cannot express: session.send's positional text fallback and
	// session.whoami's marker read from the environment.
	After func(args *ControlArgs, remaining []string)
}

// ControlVerbSpecs is the complete declaration of the control vocabulary, in
// the order the help text lists it.
func ControlVerbSpecs() []ControlVerbSpec {
	return []ControlVerbSpec{
		{
			Verb:    ControlSessionStart,
			Summary: "Session in einem Projekt oder Worktree starten",
			Flags: []ControlFlag{
				{Name: "project", Usage: "Projekt (ProjectID oder Projektname)", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "name", Usage: "Name der neuen Session", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Name = v }},
				{Name: "vendor", Usage: "Agent-Art, etwa claude oder codex", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Vendor = AgentVendor(v) }},
				{Name: "terminal", Usage: "Eine Terminal-Session ohne Coding-Agent starten", Kind: ControlFlagBool,
					SetBool: func(a *ControlArgs, v bool) {
						if v {
							a.Kind = SessionKindTerminal
						}
					}},
				{Name: "worktree", Usage: "Bestehender Worktree des Projekts (Handle)", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Worktree = v }},
				{Name: "new-worktree", Usage: "Einen frischen verwalteten Worktree anlegen", Kind: ControlFlagBool,
					SetBool: func(a *ControlArgs, v bool) { a.NewWorktree = v }},
				{Name: "dir", Usage: "Verzeichnis, das zu einem Worktree des Projekts gehören muss", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Directory = v }},
				{Name: "prompt", Usage: "Erster Prompt an den Coding-Agent", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Prompt = v }},
			},
		},
		{
			Verb:    ControlSessionList,
			Summary: "Sessions mit ihrer Beobachtung auflisten",
			Flags: []ControlFlag{
				{Name: "project", Usage: "Nur Sessions dieses Projekts auflisten", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "worktree", Usage: "Nur Sessions dieses Worktrees auflisten (Handle)", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Worktree = v }},
			},
		},
		{
			Verb:    ControlSessionSend,
			Summary: "Text an den Coding-Agent einer Session senden",
			Flags: []ControlFlag{
				{Name: "session", Usage: "SessionID oder Name", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Session = v }},
				{Name: "project", Usage: "Projekt, das einen Namen eindeutig macht", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "text", Usage: "Zu sendender Text", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Text = v }},
			},
			After: func(args *ControlArgs, remaining []string) {
				if args.Text == "" && len(remaining) > 0 {
					args.Text = strings.Join(remaining, " ")
				}
			},
		},
		{
			Verb:    ControlSessionOutput,
			Summary: "Sichtbaren Inhalt einer Session lesen",
			Flags: []ControlFlag{
				{Name: "session", Usage: "SessionID oder Name", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Session = v }},
				{Name: "project", Usage: "Projekt, das einen Namen eindeutig macht", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "lines", Usage: "Nur so viele letzte Zeilen zurückgeben", Kind: ControlFlagInt, Default: "0",
					SetInt: func(a *ControlArgs, v int) { a.Lines = v }},
			},
		},
		{
			Verb:    ControlSessionWait,
			Summary: "Auf die gepinnte Belegung einer Session warten",
			Flags: []ControlFlag{
				{Name: "session", Usage: "SessionID oder Name", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Session = v }},
				{Name: "project", Usage: "Projekt, das einen Namen eindeutig macht", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "until", Usage: "Wartebedingung: done oder waiting", Kind: ControlFlagString, Default: "done",
					SetString: func(a *ControlArgs, v string) { a.Until = v }},
				{Name: "timeout", Usage: "Zeitgrenze in Sekunden, 0 wartet ohne Grenze", Kind: ControlFlagInt, Default: "0",
					SetInt: func(a *ControlArgs, v int) { a.TimeoutMS = v * 1000 }},
			},
		},
		{
			Verb:    ControlSessionKill,
			Summary: "Runtime einer Session beenden, der Worktree bleibt",
			Flags: []ControlFlag{
				{Name: "session", Usage: "SessionID oder Name", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Session = v }},
				{Name: "project", Usage: "Projekt, das einen Namen eindeutig macht", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
			},
		},
		{
			Verb:    ControlSessionWhoami,
			Summary: "Eigene Session aus den Marker-Angaben auflösen",
			After: func(args *ControlArgs, _ []string) {
				args.Marker = ControlMarkerFromEnvironment()
			},
		},
		{
			Verb:    ControlSessionWatch,
			Summary: "Zustandswechsel als Ereignisstrom mitlesen",
			Flags: []ControlFlag{
				{Name: "project", Usage: "Nur Ereignisse dieses Projekts empfangen", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Project = v }},
				{Name: "session", Usage: "Nur Ereignisse dieser Session empfangen", Kind: ControlFlagString,
					SetString: func(a *ControlArgs, v string) { a.Session = v }},
			},
		},
	}
}

// KnownControlVerb reports whether a verb is dispatchable.
func KnownControlVerb(verb ControlVerb) bool {
	for _, known := range ControlVerbs() {
		if known == verb {
			return true
		}
	}
	return false
}

// ControlOutcome is the stable machine-readable code an agent parses. Every
// response carries exactly one; the wait outcomes are members of the same set
// so a client never has to switch on two vocabularies.
type ControlOutcome string

const (
	ControlOK ControlOutcome = "ok"
	// ControlNotFound names an address that resolves to no registered Session
	// or Project.
	ControlNotFound ControlOutcome = "not-found"
	// ControlAmbiguous names a bare name carried in more than one Project.
	ControlAmbiguous ControlOutcome = "ambiguous"
	// ControlNoTarget names a verb issued without a Session; the caller's own
	// Session is never substituted.
	ControlNoTarget ControlOutcome = "no-target"
	// ControlContainment names a directory Repositories does not resolve to a
	// Worktree of the addressed Project.
	ControlContainment ControlOutcome = "containment"
	// ControlRefused names a request the surface understood and declined, such
	// as agent input for a terminal Session.
	ControlRefused ControlOutcome = "refused"
	// ControlUnavailable names an Observation or a serving process that could
	// not be reached. It is never flattened into a plausible-looking state.
	ControlUnavailable ControlOutcome = "unavailable"
	// ControlNotManaged answers marker facts that resolve to no Session.
	ControlNotManaged ControlOutcome = "not-managed"
	// ControlUnauthorized answers a connection whose peer credentials are not
	// the owning user.
	ControlUnauthorized ControlOutcome = "unauthorized"
	// ControlInvalidRequest answers a document that is not a request.
	ControlInvalidRequest ControlOutcome = "invalid-request"
	// ControlUnknownVerb answers a verb the server does not implement.
	ControlUnknownVerb ControlOutcome = "unknown-verb"
	// ControlFailed answers a request that was understood and addressed but
	// whose execution failed.
	ControlFailed ControlOutcome = "failed"
	// ControlStalled drops a subscriber that stopped reading its events.
	ControlStalled ControlOutcome = "subscriber-stalled"

	// The wait outcomes. Exactly one of these ends a wait.
	ControlWaitDone             ControlOutcome = "done"
	ControlWaitWaiting          ControlOutcome = "waiting"
	ControlWaitBlocked          ControlOutcome = "blocked"
	ControlWaitOccupantReplaced ControlOutcome = "occupant-replaced"
	ControlWaitSessionGone      ControlOutcome = "session-gone"
	ControlWaitTimeout          ControlOutcome = "timeout"
	ControlWaitCancelled        ControlOutcome = "cancelled"
	// ControlWaitNoOccupant fails a wait on a Session with no resolvable
	// coding-agent run, immediately and without blocking.
	ControlWaitNoOccupant ControlOutcome = "no-occupant"
)

// ControlOutcomes is the complete outcome-code set.
func ControlOutcomes() []ControlOutcome {
	return []ControlOutcome{
		ControlOK, ControlNotFound, ControlAmbiguous, ControlNoTarget,
		ControlContainment, ControlRefused, ControlUnavailable, ControlNotManaged,
		ControlUnauthorized, ControlInvalidRequest, ControlUnknownVerb,
		ControlFailed, ControlStalled,
		ControlWaitDone, ControlWaitWaiting, ControlWaitBlocked,
		ControlWaitOccupantReplaced, ControlWaitSessionGone, ControlWaitTimeout,
		ControlWaitCancelled, ControlWaitNoOccupant,
	}
}

// ControlWaitOutcomes is the fixed set a wait may end with.
func ControlWaitOutcomes() []ControlOutcome {
	return []ControlOutcome{
		ControlWaitDone, ControlWaitWaiting, ControlWaitBlocked,
		ControlWaitOccupantReplaced, ControlWaitSessionGone, ControlWaitTimeout,
		ControlWaitCancelled, ControlWaitNoOccupant,
	}
}

// ControlAddressingOutcome separates an address the caller got wrong from a
// request that was refused or failed, so the CLI can exit with its own code.
func ControlAddressingOutcome(outcome ControlOutcome) bool {
	switch outcome {
	case ControlNotFound, ControlAmbiguous, ControlNoTarget, ControlContainment:
		return true
	}
	return false
}

// ControlSuccessOutcome reports whether an outcome is a success from the
// caller's point of view. A wait that observed its condition is a success; one
// that ended for any other reason is not.
func ControlSuccessOutcome(outcome ControlOutcome) bool {
	switch outcome {
	case ControlOK, ControlWaitDone, ControlWaitWaiting:
		return true
	}
	return false
}

// ControlStatus is the status vocabulary of the control surface. It is a stable
// string rather than the positional AgentStatus, which is an internal
// serialization detail the API must not leak.
type ControlStatus string

const (
	ControlStatusUnknown  ControlStatus = "unknown"
	ControlStatusRunning  ControlStatus = "running"
	ControlStatusAgents   ControlStatus = "agents"
	ControlStatusShell    ControlStatus = "shell"
	ControlStatusWaiting  ControlStatus = "waiting"
	ControlStatusIdle     ControlStatus = "idle"
	ControlStatusDone     ControlStatus = "done"
	ControlStatusExited   ControlStatus = "exited"
	ControlStatusDead     ControlStatus = "dead"
	ControlStatusTerminal ControlStatus = "terminal"
)

// controlStatus projects an observed AgentStatus onto the API vocabulary.
func controlStatus(status AgentStatus) ControlStatus {
	switch status {
	case StatusRunning:
		return ControlStatusRunning
	case StatusAgents:
		return ControlStatusAgents
	case StatusShell:
		return ControlStatusShell
	case StatusBlocked:
		return ControlStatusWaiting
	case StatusIdle:
		return ControlStatusIdle
	case StatusDone:
		return ControlStatusDone
	case StatusExited:
		return ControlStatusExited
	case StatusDead:
		return ControlStatusDead
	case StatusTerm:
		return ControlStatusTerminal
	}
	return ControlStatusUnknown
}

// ControlDelivery is the explicit applied fact of a prompt or message. An
// unknown delivery is never resent automatically.
type ControlDelivery string

const (
	ControlDeliveryNone      ControlDelivery = "none"
	ControlDeliveryDelivered ControlDelivery = "delivered"
	ControlDeliveryQueued    ControlDelivery = "queued"
	ControlDeliveryUnknown   ControlDelivery = "unknown"
	ControlDeliveryFailed    ControlDelivery = "failed"
)

// ControlMarker carries the environment-marker facts a caller presents about
// itself. It is a claim, not an authority: whoami resolves it against the
// Registry and answers not-managed when it does not resolve.
type ControlMarker struct {
	SessionID SessionID `json:"sessionId,omitempty"`
	ProjectID ProjectID `json:"projectId,omitempty"`
}

// ControlArgs is the flat argument record of every verb. One record keeps the
// wire format trivial to produce from a shell one-liner.
type ControlArgs struct {
	// Session addresses by SessionID or by name; Project qualifies a name and
	// filters a list.
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	// Worktree is a Project-qualified WorktreeRef, resolved freshly before use.
	Worktree    string `json:"worktree,omitempty"`
	NewWorktree bool   `json:"newWorktree,omitempty"`
	// Directory is only ever checked for containment, never taken on trust.
	Directory string      `json:"directory,omitempty"`
	Name      string      `json:"name,omitempty"`
	Kind      SessionKind `json:"kind,omitempty"`
	Vendor    AgentVendor `json:"vendor,omitempty"`
	Prompt    string      `json:"prompt,omitempty"`
	Text      string      `json:"text,omitempty"`
	Lines     int         `json:"lines,omitempty"`
	// Until is the wait condition: "done" or "waiting".
	Until     string        `json:"until,omitempty"`
	TimeoutMS int           `json:"timeoutMs,omitempty"`
	Marker    ControlMarker `json:"marker,omitzero"`
}

// ControlRequest is one line of the protocol. The identifier is chosen by the
// client and echoed unchanged.
type ControlRequest struct {
	ID   string      `json:"id,omitempty"`
	Verb ControlVerb `json:"verb"`
	Args ControlArgs `json:"args,omitzero"`
}

// ControlSessionView is what the surface reports about one Session. Status is
// empty whenever Availability is not available: an unreadable runtime is
// reported as unreadable, never as a plausible-looking state.
type ControlSessionView struct {
	SessionID    SessionID               `json:"sessionId"`
	Name         string                  `json:"name"`
	ProjectID    ProjectID               `json:"projectId,omitempty"`
	Project      string                  `json:"project,omitempty"`
	RuntimeName  string                  `json:"runtimeName,omitempty"`
	Dir          string                  `json:"dir,omitempty"`
	Worktree     bool                    `json:"worktree,omitempty"`
	Kind         SessionKind             `json:"kind,omitempty"`
	Vendor       AgentVendor             `json:"vendor,omitempty"`
	Availability ObservationAvailability `json:"availability,omitempty"`
	Status       ControlStatus           `json:"status,omitempty"`
	StatusSource StatusSource            `json:"statusSource,omitempty"`
}

// ControlOccupant is the pinned identity a wait is evaluated against: the
// durable SessionID, the exact RuntimeName addressed at resolution, and the
// vendor-qualified run then occupying it (ADR 0001).
type ControlOccupant struct {
	SessionID   SessionID   `json:"sessionId"`
	RuntimeName string      `json:"runtimeName"`
	Run         AgentRunRef `json:"run,omitzero"`
}

// Same reports whether two occupant identities describe the same run.
func (o ControlOccupant) Same(other ControlOccupant) bool {
	return o.SessionID == other.SessionID &&
		o.RuntimeName == other.RuntimeName &&
		o.Run == other.Run
}

// ControlResult carries the facts of whichever verb answered. Fields a verb
// does not fill stay absent rather than being reported as zero values.
type ControlResult struct {
	SessionID SessionID            `json:"sessionId,omitempty"`
	Session   *ControlSessionView  `json:"session,omitempty"`
	Sessions  []ControlSessionView `json:"sessions,omitempty"`
	// Candidates names the Sessions an ambiguous address could have meant.
	Candidates []ControlSessionView `json:"candidates,omitempty"`
	// Worktree is the Worktree a start resolved to, when it resolved one.
	Worktree     string                  `json:"worktree,omitempty"`
	WorktreeRef  WorktreeRef             `json:"worktreeRef,omitempty"`
	Dir          string                  `json:"dir,omitempty"`
	Vendor       AgentVendor             `json:"vendor,omitempty"`
	Delivery     ControlDelivery         `json:"delivery,omitempty"`
	MessageID    string                  `json:"messageId,omitempty"`
	Content      string                  `json:"content,omitempty"`
	Availability ObservationAvailability `json:"availability,omitempty"`
	Status       ControlStatus           `json:"status,omitempty"`
	Stopped      bool                    `json:"stopped,omitempty"`
	AlreadyGone  bool                    `json:"alreadyGone,omitempty"`
	// Occupant is the identity a wait pinned, echoed in its response.
	Occupant *ControlOccupant `json:"occupant,omitempty"`
	// Observed is the occupant a replacement check actually found.
	Observed *ControlOccupant `json:"observed,omitempty"`
}

// ControlResponse is one line of the protocol answering one request.
type ControlResponse struct {
	ID      string         `json:"id,omitempty"`
	Outcome ControlOutcome `json:"outcome"`
	Message string         `json:"message,omitempty"`
	Result  *ControlResult `json:"result,omitempty"`
}

// controlFailure builds a response carrying an outcome code and the German
// message a person reads.
func controlFailure(id string, outcome ControlOutcome, message string) ControlResponse {
	return ControlResponse{ID: id, Outcome: outcome, Message: message}
}

// ErrControlMalformed marks a line that is not a request document.
var ErrControlMalformed = errors.New("Steuer-Anfrage ist kein gültiges JSON-Dokument")

// DecodeControlRequest parses one protocol line. An empty verb is malformed
// rather than an unknown verb, so a client can tell a broken document from a
// verb this Magentic does not implement.
func DecodeControlRequest(line []byte) (ControlRequest, error) {
	var request ControlRequest
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	if err := decoder.Decode(&request); err != nil {
		return ControlRequest{}, ErrControlMalformed
	}
	if strings.TrimSpace(string(request.Verb)) == "" {
		return ControlRequest{}, ErrControlMalformed
	}
	return request, nil
}
