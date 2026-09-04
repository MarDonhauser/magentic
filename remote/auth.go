package remote

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// HostToken ist eine host-generierte Geräte-Anmeldedaten: 256 Bit Entropie,
// base64url-codiert. Sie authentifiziert ein Gerät, keine Person — wer sie
// hält, hält die erlaubte Aktionsfläche inklusive Terminal-Eingaben.
type HostToken string

// NewHostToken würfelt eine frische Anmeldedaten.
func NewHostToken() (HostToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return HostToken(base64.RawURLEncoding.EncodeToString(raw)), nil
}

func tokenHash(token HostToken) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

// TokenStore hält HostTokens: nur Hashes ruhen auf Platte, der Vergleich
// läuft in konstanter Zeit, jede Anmeldedaten ist einzeln widerrufbar.
type TokenStore struct {
	mu     sync.Mutex
	path   string
	hashes map[string]bool
}

// OpenTokenStore lädt den Speicher oder startet leer. Das Verzeichnis wird
// owner-only angelegt; die Datei enthält ausschließlich Hashes.
func OpenTokenStore(path string) (*TokenStore, error) {
	store := &TokenStore{path: path, hashes: map[string]bool{}}
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
	var hashes []string
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil, err
	}
	for _, hash := range hashes {
		store.hashes[hash] = true
	}
	return store, nil
}

func (s *TokenStore) persistLocked() error {
	hashes := make([]string, 0, len(s.hashes))
	for hash := range s.hashes {
		hashes = append(hashes, hash)
	}
	data, err := json.Marshal(hashes)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Issue würfelt eine Anmeldedaten und merkt sich ihren Hash. Der Klartext
// existiert danach nur noch beim Aufrufer — er wird einmalig angezeigt und
// dann nie wieder ausgegeben.
func (s *TokenStore) Issue() (HostToken, error) {
	token, err := NewHostToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes[tokenHash(token)] = true
	if err := s.persistLocked(); err != nil {
		delete(s.hashes, tokenHash(token))
		return "", err
	}
	return token, nil
}

// Valid prüft in konstanter Zeit, ohne je einen Token-Wert zu loggen.
// Verglichen werden nur SHA-256-Hashes fester Länge.
func (s *TokenStore) Valid(token HostToken) bool {
	if token == "" {
		return false
	}
	presented := tokenHash(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash := range s.hashes {
		if len(hash) != len(presented) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(hash)) == 1 {
			return true
		}
	}
	return false
}

// Revoke entzieht eine Anmeldedaten. Offene Verbindungen, die sie halten,
// schließt der Host separat (siehe Host.Revoke).
func (s *TokenStore) Revoke(token HostToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.hashes, tokenHash(token))
	return s.persistLocked()
}

// Count nennt die Zahl gültiger Anmeldedaten (für Status, nie Werte).
func (s *TokenStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hashes)
}

// RedactedAuthError meldet einen Auth-Fehlschlag, ohne je den vorgelegten
// Wert zu enthalten — weder in der Nachricht noch in Logs.
func RedactedAuthError(reason string) *WireError {
	return &WireError{Code: ErrorAuth, Message: "Anmeldung abgelehnt: " + reason}
}
