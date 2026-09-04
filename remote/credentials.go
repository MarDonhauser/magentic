package remote

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// CredentialStoreError meldet, dass der Ablageort nicht lesbar ist. Der
// Client bleibt dann detached mit ausdrücklicher Meldung und fällt niemals
// auf Klartext-Konfiguration zurück.
type CredentialStoreError struct {
	Op     string
	Reason string
}

func (e *CredentialStoreError) Error() string {
	return fmt.Sprintf("Anmeldedaten nicht lesbar (%s): %s", e.Op, e.Reason)
}

// CredentialStore birgt HostTokens außerhalb jeder Klartext-Datei. Die
// Produktion nutzt den OS-Store (macOS-Schlüsselbund, Linux-Secret-Service),
// Tests hängen den Memory-Store ein, damit sie ohne Keychain laufen.
type CredentialStore interface {
	StoreToken(ref string, token HostToken) error
	LoadToken(ref string) (HostToken, error)
	DeleteToken(ref string) error
}

// MemoryCredentialStore hält Tokens nur im Prozess — für Tests.
type MemoryCredentialStore struct {
	mu     sync.Mutex
	tokens map[string]HostToken
}

// NewMemoryCredentialStore startet einen leeren Test-Store.
func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{tokens: map[string]HostToken{}}
}

func (s *MemoryCredentialStore) StoreToken(ref string, token HostToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[ref] = token
	return nil
}

func (s *MemoryCredentialStore) LoadToken(ref string) (HostToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, known := s.tokens[ref]
	if !known {
		return "", &CredentialStoreError{Op: "lesen", Reason: "keine Anmeldedaten für " + ref + " hinterlegt"}
	}
	return token, nil
}

func (s *MemoryCredentialStore) DeleteToken(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, ref)
	return nil
}

// FailingCredentialStore simuliert einen unerreichbaren Store.
type FailingCredentialStore struct {
	Reason string
}

func (s FailingCredentialStore) StoreToken(ref string, token HostToken) error {
	return &CredentialStoreError{Op: "schreiben", Reason: s.Reason}
}

func (s FailingCredentialStore) LoadToken(ref string) (HostToken, error) {
	return "", &CredentialStoreError{Op: "lesen", Reason: s.Reason}
}

func (s FailingCredentialStore) DeleteToken(ref string) error {
	return &CredentialStoreError{Op: "löschen", Reason: s.Reason}
}

// OSCredentialStore spricht den System-Store über seine Bordmittel an:
// `security` auf macOS, `secret-tool` auf Linux. Kein neuer Daemon, keine
// neue Dependency — was das OS hergibt.
type OSCredentialStore struct {
	Service string
}

// NewDefaultCredentialStore wählt den OS-Store. Auf unbekannten Systemen
// meldet er sich als unerreichbar statt still woanders zu speichern.
func NewDefaultCredentialStore() CredentialStore {
	return OSCredentialStore{Service: "magentic-host"}
}

func (s OSCredentialStore) StoreToken(ref string, token HostToken) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "add-generic-password",
			"-s", s.Service, "-a", ref, "-w", string(token), "-U").Run()
	case "linux":
		command := exec.Command("secret-tool", "store", "--label=magentic "+ref,
			"service", s.Service, "account", ref)
		command.Stdin = strings.NewReader(string(token))
		return command.Run()
	default:
		return &CredentialStoreError{Op: "schreiben", Reason: "kein OS-Store auf " + runtime.GOOS}
	}
}

func (s OSCredentialStore) LoadToken(ref string) (HostToken, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("security", "find-generic-password",
			"-s", s.Service, "-a", ref, "-w").Output()
		if err != nil {
			return "", &CredentialStoreError{Op: "lesen", Reason: "Schlüsselbund gibt keine Anmeldedaten für " + ref + " heraus"}
		}
		return HostToken(bytes.TrimSpace(out)), nil
	case "linux":
		out, err := exec.Command("secret-tool", "lookup",
			"service", s.Service, "account", ref).Output()
		if err != nil {
			return "", &CredentialStoreError{Op: "lesen", Reason: "Secret-Service gibt keine Anmeldedaten für " + ref + " heraus"}
		}
		return HostToken(bytes.TrimSpace(out)), nil
	default:
		return "", &CredentialStoreError{Op: "lesen", Reason: "kein OS-Store auf " + runtime.GOOS}
	}
}

func (s OSCredentialStore) DeleteToken(ref string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "delete-generic-password",
			"-s", s.Service, "-a", ref).Run()
	case "linux":
		return exec.Command("secret-tool", "clear",
			"service", s.Service, "account", ref).Run()
	default:
		return &CredentialStoreError{Op: "löschen", Reason: "kein OS-Store auf " + runtime.GOOS}
	}
}
