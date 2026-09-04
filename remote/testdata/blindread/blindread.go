package blindread

import (
	"fmt"

	"magentic/remote"
)

// Dieser direkte Griff darf nicht kompilieren: Host-Fakten sind nur durch
// ihre Verfügbarkeit erreichbar. Der Test TestBlindReadDoesNotCompile
// erwartet, dass `go build` auf dieses Paket fehlschlägt.
func BlindRead() {
	view := remote.KnownView([]string{"hera"})
	fmt.Println(view.payload)
}
