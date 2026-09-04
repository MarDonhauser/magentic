package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// errPromptTargetGone nennt den einen Abbruchgrund, den auch ein
// starttoleranter Aufrufer nicht überspringen darf: die adressierte Session
// gibt es nicht mehr. Weiterzuwarten hieße, auf einen Namen zu warten, den
// inzwischen jemand anderes tragen kann.
var errPromptTargetGone = errors.New("Ziel-Session besteht nicht mehr")

// promptTarget adressiert eine Prompt-Zustellung über die SessionID. Der
// RuntimeName ist nur der zuletzt aufgelöste Transportname und wird vor jeder
// Beobachtung neu bestimmt: zwischen Einreihen und Zustellen kann die
// adressierte Session umbenannt oder entfernt worden sein, und ihr früherer
// Name kann längst einer anderen gehören. Wer über den Namen revalidiert,
// liefert den Prompt an die falsche Session.
type promptTarget struct {
	id SessionID
	// runtime ist der zuletzt bekannte RuntimeName. Er dient der Anzeige und
	// dem Warteschlangenschlüssel von Sessions ohne stabile ID; als Autorität
	// wird er nie benutzt, solange eine SessionID vorliegt.
	runtime string
	name    string
}

func promptTargetForSession(session Session) promptTarget {
	return promptTarget{id: session.ID, runtime: session.TmuxName(), name: session.Name}
}

// key ist der Schlüssel der Zustell-Warteschlange. Die stabile Identität
// gewinnt: eine wiederverwendete Laufzeitadresse erbt so keine Einträge einer
// früheren Session.
func (t promptTarget) key() string {
	if t.id != "" {
		return "session:" + string(t.id)
	}
	return "runtime:" + t.runtime
}

// label nennt das Ziel in Meldungen an den Entwickler.
func (t promptTarget) label() string {
	if t.name != "" {
		return t.name
	}
	return strings.TrimPrefix(t.runtime, SessionPrefix)
}

// resolve liest die adressierte Session frisch aus der Registry. Sie folgt
// einer Umbenennung, indem sie den aktuellen RuntimeName zurückgibt, und
// scheitert ausdrücklich, wenn die Session entfernt wurde oder ihre Identität
// nicht mehr verfügbar ist. Auf den alten Namen fällt sie nie zurück.
func (t promptTarget) resolve() (promptTarget, Session, error) {
	if t.id == "" {
		// Ein Ziel ohne stabile Identität stammt aus einem Legacy-Pfad. Es
		// bleibt beim Namen, den es mitbrachte, statt einen zu erraten.
		return t, Session{RuntimeName: t.runtime, Name: t.name}, nil
	}
	st, err := LoadState()
	if err != nil {
		return t, Session{}, fmt.Errorf("Registry für Ziel-Session %q nicht lesbar: %w", t.label(), err)
	}
	session := st.SessionByID(t.id)
	if session == nil {
		return t, Session{}, fmt.Errorf("%w: %q", errPromptTargetGone, t.label())
	}
	runtime := session.TmuxName()
	if !validRuntimeIdentity(runtime) {
		return t, Session{}, fmt.Errorf("Ziel-Session %q hat keine gültige Laufzeitadresse", session.Name)
	}
	t.runtime = runtime
	t.name = session.Name
	return t, *session, nil
}

// withRuntimeTransition hält die prozessübergreifende RuntimeName-Sperre über
// die letzte Zustellphase: letzte Beobachtung, literales Senden, optionale
// Beobachtung davor und Enter. Während der langen Bereitschaftsschleife wird
// sie bewusst nicht gehalten.
func (t promptTarget) withRuntimeTransition(runtimeName string, fn func() error) error {
	return newTransitionCoordinator(StatePath()).with(
		context.Background(), transitionScopeRuntime, []string{runtimeName},
		func(context.Context) error { return fn() },
	)
}

// observe liefert eine frische, gezielte Observation der aufgelösten Session.
// Die Observation wird über die echte SessionID verbunden, nicht über eine
// synthetische Identität, die aus einem Namen gebaut wurde.
func observeResolvedPromptTarget(ctx context.Context, session Session, observe observationReader) promptTargetObservation {
	if observe == nil {
		observe = Observe
	}
	target := session
	if target.ID == "" {
		target.ID = SessionID("prompt-target:" + session.RuntimeName)
		target.Name = strings.TrimPrefix(session.RuntimeName, SessionPrefix)
	}
	return promptTargetObservationFromSnapshot(target, observe(ctx, []Session{target}))
}
