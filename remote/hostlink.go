package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"magentic/core"
)

// HostLink beschreibt einen erreichbaren Host dauerhaft: Anzeigename,
// Netzadresse und Verweis auf die Anmeldedaten. Der Token-Wert steht nie in
// dieser Datei — nur die Referenz in den Credential-Store.
type HostLink struct {
	Name          string `json:"name"`
	Address       string `json:"address"`
	CredentialRef string `json:"credentialRef"`
}

// HostLinkStore hält HostLinks als JSON-Datei (0600). Tokенwerte kommen hier
// nie hinein — der Test unten beweist es.
type HostLinkStore struct {
	mu    sync.Mutex
	path  string
	links map[string]HostLink
}

// OpenHostLinkStore lädt oder startet leer.
func OpenHostLinkStore(path string) (*HostLinkStore, error) {
	store := &HostLinkStore{path: path, links: map[string]HostLink{}}
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
	var links []HostLink
	if err := json.Unmarshal(data, &links); err != nil {
		return nil, err
	}
	for _, link := range links {
		store.links[link.Name] = link
	}
	return store, nil
}

func (s *HostLinkStore) persistLocked() error {
	links := make([]HostLink, 0, len(s.links))
	for _, link := range s.links {
		links = append(links, link)
	}
	data, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func normalizeLink(link HostLink) (HostLink, error) {
	link.Name = strings.TrimSpace(link.Name)
	link.Address = strings.TrimSpace(link.Address)
	link.CredentialRef = strings.TrimSpace(link.CredentialRef)
	if link.Name == "" {
		return link, fmt.Errorf("HostLink braucht einen Namen")
	}
	if link.Address == "" {
		return link, fmt.Errorf("HostLink %q braucht eine Adresse", link.Name)
	}
	if link.CredentialRef == "" {
		link.CredentialRef = "host:" + link.Name
	}
	return link, nil
}

// Add legt einen HostLink an; doppelte Namen werden verweigert.
func (s *HostLinkStore) Add(link HostLink) (HostLink, error) {
	normalized, err := normalizeLink(link)
	if err != nil {
		return HostLink{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.links[normalized.Name]; taken {
		return HostLink{}, fmt.Errorf("HostLink %q gibt es bereits", normalized.Name)
	}
	s.links[normalized.Name] = normalized
	if err := s.persistLocked(); err != nil {
		delete(s.links, normalized.Name)
		return HostLink{}, err
	}
	return normalized, nil
}

// Edit ersetzt einen HostLink (auch umbenannt: alter Name raus, neuer rein).
func (s *HostLinkStore) Edit(oldName string, link HostLink) (HostLink, error) {
	normalized, err := normalizeLink(link)
	if err != nil {
		return HostLink{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, known := s.links[oldName]
	if !known {
		return HostLink{}, fmt.Errorf("HostLink %q gibt es nicht", oldName)
	}
	if normalized.Name != oldName {
		if _, taken := s.links[normalized.Name]; taken {
			return HostLink{}, fmt.Errorf("HostLink %q gibt es bereits", normalized.Name)
		}
		delete(s.links, oldName)
	}
	s.links[normalized.Name] = normalized
	if err := s.persistLocked(); err != nil {
		delete(s.links, normalized.Name)
		s.links[oldName] = previous
		return HostLink{}, err
	}
	return normalized, nil
}

// Remove löscht einen HostLink. Die Anmeldedaten im Credential-Store fasst
// das nicht an — wer den Link löscht, will den Schlüssel vielleicht
// behalten; DeleteCredential tut es ausdrücklich.
func (s *HostLinkStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, known := s.links[name]; !known {
		return fmt.Errorf("HostLink %q gibt es nicht", name)
	}
	delete(s.links, name)
	return s.persistLocked()
}

// Get holt einen Link oder meldet unbekannt.
func (s *HostLinkStore) Get(name string) (HostLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	link, known := s.links[name]
	return link, known
}

// List nennt alle Links, nach Namen sortiert.
func (s *HostLinkStore) List() []HostLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	links := make([]HostLink, 0, len(s.links))
	for _, link := range s.links {
		links = append(links, link)
	}
	for i := 1; i < len(links); i++ {
		for j := i; j > 0 && links[j].Name < links[j-1].Name; j-- {
			links[j], links[j-1] = links[j-1], links[j]
		}
	}
	return links
}

// ClientConfigDir nennt das Client-Verzeichnis neben dem State, damit
// MAGENTIC_STATE-Umleitungen (Tests) folgen.
func ClientConfigDir() string {
	return filepath.Join(filepath.Dir(core.StatePath()), "remote-client")
}
