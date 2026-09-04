package core

import (
	"fmt"
)

// ObservationTransport nennt, woher ein ObservationSnapshot stammt. Der
// leere Wert heißt lokal erzeugt und bleibt das Verhalten für alles, was
// heute schon Snapshots liest.
type ObservationTransport string

const (
	// ObservationTransportRemote markiert Snapshots, die ein Host über das
	// Netz geliefert hat. Die hostseitige Unverfügbarkeits-Begründung
	// (Availability + Problems) reist unverändert mit; der Transport fügt
	// nur die Herkunft hinzu, statt sie zu überschreiben.
	ObservationTransportRemote ObservationTransport = "remote"
)

// RequireFreshObservation ist das ADR-0004-Gate für alles, was nur auf frisch
// bekannten Fakten laufen darf — lokal wie remote dieselbe Regel (D4): Eine
// Entfernung, ein Kill oder ein Merge braucht einen verfügbaren Snapshot.
// Ein unavailable/partial Snapshot verweigert mit Begründung, statt aus
// Unwissen zu zerstören.
func RequireFreshObservation(snapshot ObservationSnapshot) error {
	if snapshot.Availability != ObservationAvailable {
		if snapshot.TransportProblem != "" {
			return fmt.Errorf("keine frischen Fakten: %s", snapshot.TransportProblem)
		}
		if len(snapshot.Problems) > 0 {
			return fmt.Errorf("keine frischen Fakten: %s: %s",
				snapshot.Problems[0].Operation, snapshot.Problems[0].Message)
		}
		return fmt.Errorf("keine frischen Fakten: Observation ist %s, nicht verfügbar", snapshot.Availability)
	}
	return nil
}
