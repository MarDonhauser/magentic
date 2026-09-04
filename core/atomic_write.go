package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// writeJSONFileAtomically schreibt einen durablen Datensatz so, dass ein
// Leser entweder den alten oder den neuen Stand sieht und nie einen halben:
// in eine Temporärdatei im Zielverzeichnis, mit fsync, dann rename, dann fsync
// des Verzeichnisses. Registry und Lifecycle-Ledger teilen sich diese
// Implementation — vorher stand sie zweimal da und musste zweimal stimmen.
func writeJSONFileAtomically(path, tempPattern string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
