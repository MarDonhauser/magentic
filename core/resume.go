package core

import (
	"fmt"
	"os"
	"strings"
)

// ResumableStatusLabel and ResumableStatusIcon render the resumable reading in
// every surface. The copy stays German like the rest of the UI and never
// claims the agent's process survived: the conversation is resumed, not the
// process.
const (
	ResumableStatusLabel = "Fortsetzbar"
	ResumableStatusIcon  = "↻"
)

// SessionResumability is the derived reading of a Session whose external
// runtime is absent: either resumable, when its durable record carries
// everything a resume needs, or dead with the reason stated. It is computed
// from the durable record plus the current Observation, exactly like the other
// status readings, and never stored.
type SessionResumability struct {
	// Resumable offers a way back: resume the stored conversation, or start
	// fresh in the recorded directory when FreshOnly is set.
	Resumable bool
	// FreshOnly rewords the offer to "start fresh here": the vendor cannot
	// restore the stored conversation (or no longer holds it), so no wording
	// may claim the conversation is restored.
	FreshOnly bool
	// FreshReason states why a resumable Session only starts fresh. Empty for
	// vendors that never resume by reference.
	FreshReason string
	// Reason states why a Session is not resumable. Empty for live Sessions,
	// which are neither resumable nor dead.
	Reason string
	// Unknown marks an unobservable runtime: neither resumable nor dead may be
	// claimed, and neither resume nor discard is offered.
	Unknown bool
}

// ClassifyResumability derives the resumable reading for one Session. A nil
// provider means the agent kind is unknown. A nil dirExists falls back to the
// filesystem, so tests pass a stub while production reads the real directory.
func ClassifyResumability(session Session, observed SessionObservation, provider AgentProvider, dirExists func(string) bool) SessionResumability {
	if observed.Presence == SessionPresencePresent {
		return SessionResumability{}
	}
	if observed.Presence != SessionPresenceAbsent {
		return SessionResumability{Unknown: true, Reason: "Laufzeit-Präsenz ist derzeit unbekannt"}
	}
	if !session.LaterAt.IsZero() {
		return SessionResumability{Reason: "Session ist für später abgelegt"}
	}
	if session.IsTerm() {
		return SessionResumability{Reason: "Terminal-Session ohne Coding-Agent"}
	}
	// Nur tmux-Runtimes werden über einen frischen tmux-Prozess fortgesetzt.
	// Eine verwaltete Runtime gehört dem Daemon-Protokoll, nicht tmux.
	if session.SessionRuntime() != RuntimeTmux {
		return SessionResumability{Reason: "verwaltete Runtime ohne Wiederaufnahme"}
	}
	if provider == nil {
		return SessionResumability{Reason: fmt.Sprintf("unbekannter Agent-Vendor %q", string(session.SessionVendor()))}
	}
	dir := strings.TrimSpace(session.Dir)
	if dir == "" || dir == "." {
		return SessionResumability{Reason: "kein Arbeitsverzeichnis verzeichnet"}
	}
	exists := dirExists
	if exists == nil {
		exists = defaultResumeDirExists
	}
	if !exists(session.Dir) {
		return SessionResumability{Reason: fmt.Sprintf("Arbeitsverzeichnis %q fehlt", ShortPath(session.Dir))}
	}
	switch provider.ResumeBehavior() {
	case ResumeFreshOnly:
		return SessionResumability{Resumable: true, FreshOnly: true}
	case ResumeUnsupported:
		return SessionResumability{Reason: fmt.Sprintf("%s kann keine Konversation wiederaufnehmen", provider.Tool())}
	case ResumeByRunRef:
		run, ok := session.AgentRun(provider.Vendor())
		if !ok {
			return SessionResumability{Reason: "keine gespeicherte Konversation"}
		}
		if !provider.RunExists(run.ExternalID) {
			return SessionResumability{
				Resumable: true, FreshOnly: true,
				FreshReason: "gespeicherte Konversation ist beim Anbieter nicht mehr vorhanden",
			}
		}
		return SessionResumability{Resumable: true}
	default:
		return SessionResumability{Reason: fmt.Sprintf("%s meldet ein unbekanntes Resume-Verhalten", provider.Tool())}
	}
}

func defaultResumeDirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// ResumabilityForSession derives the resumable reading with the production
// dependencies: the Session's own provider and the real filesystem.
func ResumabilityForSession(session Session, observed SessionObservation) SessionResumability {
	provider, _ := resolveSessionProvider(session)
	return ClassifyResumability(session, observed, provider, nil)
}

// ResumeLastSeen renders what a resumable Session was last doing and when,
// for example "zuletzt: wartet · vor 2h05m". The explicit past anchoring is
// what keeps a running last status from reading as a surviving process. A
// Session never observed carries no time and renders empty, never a status.
func ResumeLastSeen(session Session) string {
	if session.LastStatusAt.IsZero() {
		return ""
	}
	if session.LastStatus == StatusUnknown {
		return "zuletzt gesehen " + FormatAgeWord(session.LastStatusAt)
	}
	return "zuletzt: " + session.LastStatus.Label() + " · " + FormatAgeWord(session.LastStatusAt)
}

// Session action identities offered for absent runtimes.
const (
	SessionActionResume       = "resume"
	SessionActionResumeFresh  = "resume-fresh"
	SessionActionRestartShell = "restart-shell"
	SessionActionDiscard      = "discard"
)

// SessionAction is one honest offer for a Session without a runtime, with the
// German copy the TUI and the desktop app render as-is.
type SessionAction struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// SessionActionsFor produces the actions one reading is offered. Live Sessions
// keep their existing actions and unknown availability offers none: the
// returned list only ever covers the absent-runtime readings.
func SessionActionsFor(session Session, observed SessionObservation, res SessionResumability) []SessionAction {
	if res.Unknown || observed.Presence != SessionPresenceAbsent {
		return nil
	}
	if !session.LaterAt.IsZero() {
		return nil
	}
	if session.IsTerm() {
		return []SessionAction{{
			ID: SessionActionRestartShell, Label: "Shell neu starten",
			Detail: fmt.Sprintf("neue Shell in %s", ShortPath(session.Dir)),
		}}
	}
	if !res.Resumable {
		return nil
	}
	discard := SessionAction{
		ID: SessionActionDiscard, Label: "Verwerfen",
		Detail: "Eintrag entfernen, Verzeichnis bleibt erhalten",
	}
	if res.FreshOnly {
		detail := res.FreshReason
		if detail == "" {
			detail = "startet immer eine frische Konversation"
		}
		return []SessionAction{
			{ID: SessionActionResumeFresh, Label: "Frisch starten", Detail: detail},
			discard,
		}
	}
	resume := SessionAction{ID: SessionActionResume, Label: "Fortsetzen"}
	if seen := ResumeLastSeen(session); seen != "" {
		resume.Detail = seen
	}
	return []SessionAction{resume, discard}
}
