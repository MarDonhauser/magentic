package remote

import (
	"os"
	"strings"
	"testing"
)

// Links überstehen einen App-Neustart: Anlegen, Ändern, Löschen — und die
// Datei enthält danach nie einen Token-Wert.
func TestHostLinkPersistence(t *testing.T) {
	path := t.TempDir() + "/hosts.json"
	store, err := OpenHostLinkStore(path)
	if err != nil {
		t.Fatal(err)
	}
	link, err := store.Add(HostLink{Name: "atelier", Address: "100.64.0.2:8443", CredentialRef: "host:atelier"})
	if err != nil {
		t.Fatal(err)
	}
	if link.CredentialRef != "host:atelier" {
		t.Errorf("Referenz verloren: %+v", link)
	}
	edited, err := store.Edit("atelier", HostLink{Name: "atelier", Address: "100.64.0.3:8443", CredentialRef: "host:atelier"})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Address != "100.64.0.3:8443" {
		t.Errorf("Änderung verloren: %+v", edited)
	}

	reopened, err := OpenHostLinkStore(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, known := reopened.Get("atelier")
	if !known || restored.Address != "100.64.0.3:8443" {
		t.Errorf("Neustart verliert den Link: %+v", restored)
	}
	if err := reopened.Remove("atelier"); err != nil {
		t.Fatal(err)
	}
	if _, known := reopened.Get("atelier"); known {
		t.Error("gelöschter Link lebt weiter")
	}
	// Persistiert steht nur die Referenz — nie ein Token-Wert.
	secret, _ := NewHostToken()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), string(secret)) {
		t.Error("Token-Wert in der Konfiguration")
	}
	if _, err := reopened.Add(HostLink{Name: "atelier", Address: "x"}); err != nil {
		t.Fatalf("erneutes Anlegen scheitert: %v", err)
	}
	if _, err := reopened.Add(HostLink{Name: "atelier", Address: "y"}); err == nil {
		t.Error("doppelter Name akzeptiert")
	}
}

// Der geschriebene Link trägt nur die Referenz; der Token-Wert lebt allein
// im Credential-Store. Ein unerreichbarer Store hält die App detached mit
// ausdrücklicher Meldung statt auf Klartext zurückzufallen.
func TestTokenLivesOutsideConfig(t *testing.T) {
	path := t.TempDir() + "/hosts.json"
	store, err := OpenHostLinkStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(HostLink{Name: "atelier", Address: "127.0.0.1:8443"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "host:atelier") {
		t.Errorf("Referenz fehlt in der Konfiguration: %s", raw)
	}
	credentials := NewMemoryCredentialStore()
	secret, _ := NewHostToken()
	if err := credentials.StoreToken("host:atelier", secret); err != nil {
		t.Fatal(err)
	}
	loaded, err := credentials.LoadToken("host:atelier")
	if err != nil || loaded != secret {
		t.Errorf("Store gibt Token nicht heraus: %v", err)
	}
	failing := FailingCredentialStore{Reason: "Schlüsselbund gesperrt"}
	if _, err := failing.LoadToken("host:atelier"); err == nil {
		t.Fatal("unerreichbarer Store meldet keinen Fehler")
	} else if storeErr, ok := err.(*CredentialStoreError); !ok || !strings.Contains(storeErr.Error(), "Schlüsselbund gesperrt") {
		t.Errorf("undeutliche Meldung: %v", err)
	}
}

// Geändertes Zertifikat wird verweigert, nicht still angenommen.
func TestFingerprintPinRefusesChange(t *testing.T) {
	pins, err := OpenFingerprintStore(t.TempDir() + "/pins.json")
	if err != nil {
		t.Fatal(err)
	}
	pinned, changed, err := pins.Check("100.64.0.2:8443", "fp-eins")
	if err != nil || changed || !pinned {
		t.Errorf("erstes Sehen pinnt nicht: %v %v %v", pinned, changed, err)
	}
	pinned, changed, err = pins.Check("100.64.0.2:8443", "fp-eins")
	if err != nil || changed || pinned {
		t.Errorf("gleicher Fingerprint irritiert: %v %v %v", pinned, changed, err)
	}
	if _, changed, err := pins.Check("100.64.0.2:8443", "fp-zwei"); err == nil || !changed {
		t.Error("geändertes Zertifikat still angenommen")
	}
}
