package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZgData(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".zeitgeist")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestZeitgeistLifecycle(t *testing.T) {
	writeZgData(t, `{"projects":[{"id":"p1","name":"Spedition","client":"Irtz","rate":90,"color":"#fff","extra":"bleibt"}],"sessions":[],"current":null}`)

	p, err := ZeitgeistStart("sped")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Spedition" || p.Rate != 90 {
		t.Fatalf("start: %+v", p)
	}
	if _, err := ZeitgeistStart("sped"); err == nil {
		t.Fatal("zweiter Start sollte fehlschlagen")
	}

	info := ZeitgeistInfo()
	if !info.Exists || !info.Active || info.State != "running" || info.Project != "Spedition" {
		t.Fatalf("info: %+v", info)
	}

	if err := ZeitgeistPause(); err != nil {
		t.Fatal(err)
	}
	if ZeitgeistInfo().State != "paused" {
		t.Fatal("nicht pausiert")
	}
	if err := ZeitgeistResume(); err != nil {
		t.Fatal(err)
	}

	s, err := ZeitgeistStop("kurzer test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "Spedition" {
		t.Fatalf("stop: %+v", s)
	}
	if ZeitgeistInfo().Active {
		t.Fatal("nach Stop noch aktiv")
	}

	data, err := os.ReadFile(ZeitgeistFile())
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if !strings.Contains(raw, `"extra"`) {
		t.Fatalf("unbekanntes Projektfeld verloren: %s", raw)
	}
	if !strings.Contains(raw, `"summary": "kurzer test"`) {
		t.Fatalf("Notiz fehlt: %s", raw)
	}
	if !strings.Contains(raw, `"current": null`) {
		t.Fatalf("current nicht null: %s", raw)
	}
}

func TestZeitgeistInfoNoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	info := ZeitgeistInfo()
	if info.Exists || info.Active {
		t.Fatalf("info: %+v", info)
	}
}

func TestZeitgeistPausedDuration(t *testing.T) {
	writeZgData(t, `{"projects":[{"id":"p1","name":"X","rate":60,"color":"#fff"}],"sessions":[],
		"current":{"id":"c1","projectId":"p1","start":"2026-07-17T08:00:00.000Z",
		"pauses":[{"start":"2026-07-17T08:30:00.000Z","end":"2026-07-17T08:40:00.000Z"}],
		"state":"running","rate":60}}`)
	f, err := zgLoad()
	if err != nil {
		t.Fatal(err)
	}
	at := zgParseTime("2026-07-17T09:00:00.000Z")
	if sec := zgElapsedSec(f.Current, at); sec != 50*60 {
		t.Fatalf("elapsed: %d", sec)
	}
	if e := zgEarnings(50*60, 60); e != 50 {
		t.Fatalf("earnings: %v", e)
	}
}
