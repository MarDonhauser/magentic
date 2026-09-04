package remote

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RejectClientPath verweigert Client-gelieferte Dateisystempfade an der
// Host-Grenze, bevor irgendetwas das Dateisystem berührt. Remote-Aktionen
// adressieren Worktrees und Specifications ausschließlich über host-aufgelöste
// Handles (WorktreeRef, SpecificationStartToken, ProjectID); ein String, der
// wie ein Pfad aussieht, wird nicht als solcher interpretiert, sondern
// abgewiesen.
func RejectClientPath(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.ContainsRune(trimmed, 0) {
		return fmt.Errorf("Pfad-Eingabe enthält ein NUL-Byte")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "~") {
		return fmt.Errorf("Client-Pfad %q wird nicht akzeptiert — host-aufgelöste Handles verwenden", trimmed)
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		return fmt.Errorf("Client-Pfad %q wird nicht akzeptiert — host-aufgelöste Handles verwenden", trimmed)
	}
	if strings.HasPrefix(trimmed, `\\`) {
		return fmt.Errorf("Client-Pfad %q wird nicht akzeptiert — host-aufgelöste Handles verwenden", trimmed)
	}
	for _, segment := range strings.Split(filepath.ToSlash(trimmed), "/") {
		if segment == ".." {
			return fmt.Errorf("Client-Pfad %q wird nicht akzeptiert — host-aufgelöste Handles verwenden", trimmed)
		}
	}
	if strings.ContainsAny(trimmed, `/\`) {
		return fmt.Errorf("Client-Pfad %q wird nicht akzeptiert — host-aufgelöste Handles verwenden", trimmed)
	}
	return nil
}
