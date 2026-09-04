package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FingerprintStore merkt sich je Adresse den gepinnten TLS-Fingerprint
// (TOFU): Beim ersten Attach gepinnt, danach verweigert jede Änderung den
// Aufbau statt still akzeptiert zu werden.
type FingerprintStore struct {
	mu   sync.Mutex
	path string
	pins map[string]string
}

// OpenFingerprintStore lädt oder startet leer.
func OpenFingerprintStore(path string) (*FingerprintStore, error) {
	store := &FingerprintStore{path: path, pins: map[string]string{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store.pins); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FingerprintStore) persistLocked() error {
	data, err := json.MarshalIndent(s.pins, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Check pinnt beim ersten Sehen und prüft danach. changed=true heißt: Der
// Fingerprint wich vom gemerkten ab — der Aufrufer verweigert die
// Verbindung, statt sie still anzunehmen.
func (s *FingerprintStore) Check(address, fingerprint string) (pinned bool, changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	known, exists := s.pins[address]
	if !exists {
		s.pins[address] = fingerprint
		if err := s.persistLocked(); err != nil {
			delete(s.pins, address)
			return false, false, err
		}
		return true, false, nil
	}
	if known != fingerprint {
		return false, true, fmt.Errorf("TLS-Fingerprint von %s hat sich geändert — Verbindung verweigert", address)
	}
	return false, false, nil
}

// Pinned nennt den gemerkten Fingerprint einer Adresse.
func (s *FingerprintStore) Pinned(address string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fingerprint, known := s.pins[address]
	return fingerprint, known
}

// Forget löst einen Pin (bewusst, nach Betreiber-Prüfung).
func (s *FingerprintStore) Forget(address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pins, address)
	return s.persistLocked()
}
