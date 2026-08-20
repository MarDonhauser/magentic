package core

import (
	"strings"
	"unicode"
)

// validRuntimeIdentity is the shared authority check applied before an opaque
// RuntimeName crosses an external process boundary. RuntimeName values are not
// display labels: normalizing one can address a different tmux Session.
func validRuntimeIdentity(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
