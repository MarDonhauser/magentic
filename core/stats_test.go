package core

import "testing"

func TestOwnCommit(t *testing.T) {
	const email = "martin.donhauser@lhind.dlh.de"
	const name = "donhauser, martin"

	cases := []struct {
		label  string
		cEmail string
		cName  string
		want   bool
	}{
		{"eigene Mail", "martin.donhauser@lhind.dlh.de", "DONHAUSER, MARTIN", true},
		{"Mail in anderer Schreibweise", "Martin.Donhauser@LHIND.dlh.de", "irgendwer", true},
		{"nur Name passt", "privat@example.com", "DONHAUSER, MARTIN", true},
		{"fremder Commit", "kai@example.com", "Kai Detmers", false},
		{"Leerzeichen drumherum", " martin.donhauser@lhind.dlh.de ", "x", true},
	}
	for _, c := range cases {
		if got := ownCommit(c.cEmail, c.cName, email, name); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.label, got, c.want)
		}
	}
}

func TestOwnCommitOhneIdentitaet(t *testing.T) {
	if !ownCommit("wer@auch.immer", "Wer Auch Immer", "", "") {
		t.Fatal("ohne konfigurierte Identität muss alles zählen")
	}
}

func TestIsCommandNoise(t *testing.T) {
	cases := []struct {
		label string
		line  string
		want  bool
	}{
		{"Slash-Kommando", `{"type":"user","message":{"content":"<command-name>/model</command-name>"}}`, true},
		{"Kommando-Ausgabe", `{"type":"user","message":{"content":"<local-command-stdout>Set model to Fable 5</local-command-stdout>"}}`, true},
		{"Fehlerausgabe", `{"type":"user","message":{"content":"<local-command-stderr>boom</local-command-stderr>"}}`, true},
		{"echter Prompt", `{"type":"user","message":{"content":"mach mal die statistik"}}`, false},
		{"Prompt der über Kommandos redet", `{"type":"user","message":{"content":"wie funktioniert /done?"}}`, false},
	}
	for _, c := range cases {
		if got := isCommandNoise([]byte(c.line)); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.label, got, c.want)
		}
	}
}
