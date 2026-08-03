package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var breakStart = time.Date(2026, 8, 3, 9, 0, 0, 0, time.Local)

func useTempBreaks(t *testing.T) string {
	t.Helper()
	sp := useTempState(t)
	resetBreakState()
	t.Cleanup(resetBreakState)
	return filepath.Join(filepath.Dir(sp), "breaks.json")
}

func fakeNow(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = time.Now })
	return &cur
}

func workMinutes(t *testing.T, clk *time.Time, from time.Time, mins int) BreakAdvice {
	t.Helper()
	*clk = from
	a := BreakHeartbeat(true)
	for m := 1; m <= mins; m++ {
		*clk = from.Add(time.Duration(m) * time.Minute)
		a = BreakHeartbeat(true)
	}
	return a
}

func readBreakFile(t *testing.T, p string) breakFile {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("breaks.json nicht lesbar: %v", err)
	}
	var f breakFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("breaks.json ist kein gültiges JSON: %v\n%s", err, data)
	}
	return f
}

func TestWorkBlockGrows(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)

	if a := BreakHeartbeat(true); a.WorkedSecs != 0 || a.Level != BreakLevelNone {
		t.Fatalf("Start: %d s, Level %s", a.WorkedSecs, a.Level)
	}
	*clk = breakStart.Add(5 * time.Minute)
	if a := BreakHeartbeat(true); a.WorkedSecs != 300 {
		t.Fatalf("nach 5 Minuten: %d s", a.WorkedSecs)
	}
	*clk = breakStart.Add(10 * time.Minute)
	a := BreakHeartbeat(true)
	if a.WorkedSecs != 600 {
		t.Fatalf("nach 10 Minuten: %d s", a.WorkedSecs)
	}
}

func TestIdleBeyondResetEndsBlockAndBooksAutoBreak(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)

	if a := workMinutes(t, clk, breakStart, 50); a.Level != BreakLevelHint {
		t.Fatalf("nach 50 Minuten erwartet hint, ist %s", a.Level)
	}

	*clk = breakStart.Add(57 * time.Minute)
	a := BreakHeartbeat(false)
	if a.Level != BreakLevelResting {
		t.Fatalf("7 Minuten ohne Aktivität müssen als Ruhephase gelten, Level %s", a.Level)
	}
	if a.WorkedSecs != 0 {
		t.Fatalf("Arbeitsblock läuft weiter: %d s", a.WorkedSecs)
	}
	if a.RestingSecs != 7*60 {
		t.Fatalf("Ruhedauer %d s", a.RestingSecs)
	}

	*clk = breakStart.Add(70 * time.Minute)
	a = BreakHeartbeat(true)
	if breakLogSnapshot().BreaksToday != 1 {
		t.Fatalf("Rückkehr muss eine Auto-Pause verbucht haben, BreaksToday=%d", breakLogSnapshot().BreaksToday)
	}
	if breakLogSnapshot().BreakSecsToday != 20*60 {
		t.Fatalf("Pausenlänge %d s, erwartet %d", breakLogSnapshot().BreakSecsToday, 20*60)
	}
	if a.WorkedSecs != 0 || a.Level != BreakLevelNone {
		t.Fatalf("neuer Arbeitsblock startet nicht bei 0: %d s / %s", a.WorkedSecs, a.Level)
	}
	if breakLogSnapshot().WorkSecsToday != 50*60 {
		t.Fatalf("Arbeitszeit heute %d s, erwartet %d", breakLogSnapshot().WorkSecsToday, 50*60)
	}

}

func TestShortIdleKeepsWorkBlock(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 50)

	*clk = breakStart.Add(53 * time.Minute)
	a := BreakHeartbeat(false)
	if a.Level != BreakLevelHint {
		t.Fatalf("3 Minuten Ruhe dürfen den Block nicht beenden, Level %s", a.Level)
	}
	if a.WorkedSecs != 53*60 {
		t.Fatalf("Arbeitsblock %d s, erwartet %d", a.WorkedSecs, 53*60)
	}
	if a.RestingSecs != 3*60 {
		t.Fatalf("Ruhedauer %d s", a.RestingSecs)
	}
	if breakLogSnapshot().BreaksToday != 0 {
		t.Fatalf("kurze Ruhe darf keine Pause verbuchen: %d", breakLogSnapshot().BreaksToday)
	}

	*clk = breakStart.Add(54 * time.Minute)
	a = BreakHeartbeat(true)
	if a.WorkedSecs != 54*60 || a.RestingSecs != 0 {
		t.Fatalf("nach Rückkehr: %d s Arbeit, %d s Ruhe", a.WorkedSecs, a.RestingSecs)
	}
}

func TestLevelThresholds(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	*clk = breakStart
	BreakHeartbeat(true)

	for m := 1; m <= 85; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		a := BreakHeartbeat(true)
		want := BreakLevelNone
		switch {
		case m >= 80:
			want = BreakLevelOverdue
		case m >= 55:
			want = BreakLevelDue
		case m >= 40:
			want = BreakLevelHint
		}
		if a.Level != want {
			t.Fatalf("nach %d Minuten Level %s, erwartet %s", m, a.Level, want)
		}
		if a.Message == "" {
			t.Fatalf("nach %d Minuten fehlt die Meldung", m)
		}
	}
}

func TestLevelBoundaryIsExactMinute(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 39)

	*clk = breakStart.Add(40*time.Minute - time.Second)
	if a := BreakHeartbeat(true); a.Level != BreakLevelNone {
		t.Fatalf("bei 39:59 noch %s", a.Level)
	}
	*clk = breakStart.Add(40 * time.Minute)
	a := BreakHeartbeat(true)
	if a.Level != BreakLevelHint {
		t.Fatalf("bei 40:00 erwartet hint, ist %s", a.Level)
	}
	if a.NextDueSecs != 15*60 {
		t.Fatalf("NextDueSecs %d, erwartet %d", a.NextDueSecs, 15*60)
	}
}

func TestSnoozeCapsLevel(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	if a := workMinutes(t, clk, breakStart, 60); a.Level != BreakLevelDue {
		t.Fatalf("nach 60 Minuten erwartet due, ist %s", a.Level)
	}
	if err := SnoozeBreak(); err != nil {
		t.Fatal(err)
	}

	*clk = breakStart.Add(61 * time.Minute)
	a := BreakHeartbeat(true)
	if a.Level != BreakLevelHint || !a.Snoozed {
		t.Fatalf("Snooze deckelt nicht: Level %s, Snoozed %v", a.Level, a.Snoozed)
	}
	if a.NextDueSecs != 9*60 {
		t.Fatalf("NextDueSecs während Snooze %d, erwartet %d", a.NextDueSecs, 9*60)
	}

	for m := 62; m <= 71; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		a = BreakHeartbeat(true)
	}
	if a.Level != BreakLevelDue || a.Snoozed {
		t.Fatalf("nach Ablauf des Snooze: Level %s, Snoozed %v", a.Level, a.Snoozed)
	}
}

func TestSnoozeCannotHideOverdueForever(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 85)
	if err := SnoozeBreak(); err != nil {
		t.Fatal(err)
	}
	*clk = breakStart.Add(86 * time.Minute)
	if a := BreakHeartbeat(true); a.Level != BreakLevelHint {
		t.Fatalf("Snooze muss auch overdue deckeln, Level %s", a.Level)
	}
	for m := 87; m <= 96; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		BreakHeartbeat(true)
	}
	if a := BreakHeartbeat(true); a.Level != BreakLevelOverdue {
		t.Fatalf("nach dem Snooze muss overdue zurückkommen, Level %s", a.Level)
	}
}

func TestGoodMomentConstellations(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}, {Name: "atlas"}}}

	cases := []struct {
		name     string
		statuses map[string]AgentStatus
		good     bool
		busy     int
		waiting  int
	}{
		{"nur rechnende", map[string]AgentStatus{"hera": StatusRunning, "atlas": StatusAgents}, true, 2, 0},
		{"eine wartende", map[string]AgentStatus{"hera": StatusRunning, "atlas": StatusBlocked}, false, 1, 1},
		// Eine fertige Session verlangt keine Handlung — sie darf den guten
		// Moment nicht kippen, sonst kommt der Vorschlag nie.
		{"eine idle", map[string]AgentStatus{"hera": StatusRunning, "atlas": StatusIdle}, true, 1, 0},
		{"gar keine", map[string]AgentStatus{}, false, 0, 0},
		{"alle tot", map[string]AgentStatus{"hera": StatusDead, "atlas": StatusDead}, false, 0, 0},
		{"Hintergrund-Shell zählt als beschäftigt", map[string]AgentStatus{"hera": StatusShell}, true, 1, 0},
	}
	for _, c := range cases {
		a := BreakStatus(st, c.statuses, nil)
		if a.GoodMoment != c.good || a.Busy != c.busy || a.Waiting != c.waiting {
			t.Fatalf("%s: good=%v busy=%d waiting=%d, erwartet good=%v busy=%d waiting=%d",
				c.name, a.GoodMoment, a.Busy, a.Waiting, c.good, c.busy, c.waiting)
		}
	}
}

func TestGoodMomentIgnoresUnbekannteSessions(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}}}
	a := BreakStatus(st, map[string]AgentStatus{"hera": StatusRunning, "verwaist": StatusBlocked}, nil)
	if !a.GoodMoment || a.Waiting != 0 {
		t.Fatalf("Sessions ohne Agent im State dürfen nicht zählen: good=%v waiting=%d", a.GoodMoment, a.Waiting)
	}
}

func TestAgentComputeIsNoUserActivity(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}}}
	running := map[string]AgentStatus{"hera": StatusRunning}

	*clk = breakStart
	BreakHeartbeat(true)

	for m := 1; m <= 5; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		BreakStatus(st, running, map[string]time.Time{"hera": *clk})
	}
	*clk = breakStart.Add(7 * time.Minute)
	a := BreakStatus(st, running, map[string]time.Time{"hera": *clk})
	if a.Level != BreakLevelResting || a.WorkedSecs != 0 {
		t.Fatalf("rechnende Agents halten den Arbeitsblock am Leben: Level %s, %d s", a.Level, a.WorkedSecs)
	}
}

func TestWaitingSessionActivityCountsAsUser(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}}}
	blocked := map[string]AgentStatus{"hera": StatusBlocked}

	*clk = breakStart
	BreakHeartbeat(true)

	for m := 1; m <= 7; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		BreakStatus(st, blocked, map[string]time.Time{"hera": *clk})
	}
	a := BreakStatus(st, blocked, map[string]time.Time{"hera": *clk})
	if a.WorkedSecs != 7*60 {
		t.Fatalf("Tippen in einer wartenden Session muss den Block halten: %d s", a.WorkedSecs)
	}
	if a.Level == BreakLevelResting {
		t.Fatal("Block wurde fälschlich beendet")
	}
}

func TestFirstSightingOfSessionIsNoActivity(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}}}

	*clk = breakStart
	BreakHeartbeat(true)
	*clk = breakStart.Add(7 * time.Minute)
	a := BreakStatus(st, map[string]AgentStatus{"hera": StatusBlocked},
		map[string]time.Time{"hera": breakStart.Add(6 * time.Minute)})
	if a.Level != BreakLevelResting {
		t.Fatalf("die erste beobachtete Aktivität ist nur ein Ausgangswert, Level %s", a.Level)
	}
}

func TestTakeAndEndBreak(t *testing.T) {
	p := useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 30)

	if err := TakeBreak(); err != nil {
		t.Fatal(err)
	}
	if a := BreakHeartbeat(false); a.Level != BreakLevelResting || a.WorkedSecs != 0 {
		t.Fatalf("nach TakeBreak: Level %s, %d s Arbeit", a.Level, a.WorkedSecs)
	}

	*clk = breakStart.Add(45 * time.Minute)
	a := BreakHeartbeat(false)
	if a.Level != BreakLevelResting {
		t.Fatalf("laufende Pause darf nicht von der Idle-Erkennung überschrieben werden, Level %s", a.Level)
	}
	if a.RestingSecs != 15*60 {
		t.Fatalf("Pausendauer %d s", a.RestingSecs)
	}
	if !strings.Contains(a.Message, "15") {
		t.Fatalf("Meldung nennt die Minuten nicht: %q", a.Message)
	}

	if err := EndBreak(); err != nil {
		t.Fatal(err)
	}
	a = BreakHeartbeat(true)
	if breakLogSnapshot().BreaksToday != 1 || breakLogSnapshot().BreakSecsToday != 15*60 {
		t.Fatalf("Pause nicht verbucht: %d Pausen, %d s", breakLogSnapshot().BreaksToday, breakLogSnapshot().BreakSecsToday)
	}
	if a.Level != BreakLevelNone || a.WorkedSecs != 0 {
		t.Fatalf("nach der Pause startet kein frischer Block: %s / %d s", a.Level, a.WorkedSecs)
	}
	if breakLogSnapshot().WorkSecsToday != 30*60 {
		t.Fatalf("Arbeitszeit heute %d s", breakLogSnapshot().WorkSecsToday)
	}

	f := readBreakFile(t, p)
	if len(f.Log.History) != 1 || f.Log.History[0].Kind != BreakKindTaken {
		t.Fatalf("Historie: %+v", f.Log.History)
	}
	if f.Log.History[0].WorkSecs != 30*60 {
		t.Fatalf("Arbeitsblock vor der Pause: %d s", f.Log.History[0].WorkSecs)
	}
}

func TestEndBreakWithoutBreakDoesNothing(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 10)

	if err := EndBreak(); err != nil {
		t.Fatal(err)
	}
	a := BreakHeartbeat(true)
	if breakLogSnapshot().BreaksToday != 0 {
		t.Fatalf("EndBreak ohne Pause hat gezählt: %d", breakLogSnapshot().BreaksToday)
	}
	if a.WorkedSecs != 10*60 {
		t.Fatalf("Arbeitsblock wurde angetastet: %d s", a.WorkedSecs)
	}
}

func TestDayRolloverResetsCounters(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	workMinutes(t, clk, breakStart, 30)
	if err := TakeBreak(); err != nil {
		t.Fatal(err)
	}
	*clk = breakStart.Add(40 * time.Minute)
	if err := EndBreak(); err != nil {
		t.Fatal(err)
	}
	BreakHeartbeat(true)
	if l := breakLogSnapshot(); l.BreaksToday != 1 || l.WorkSecsToday != 30*60 {
		t.Fatalf("vor dem Tageswechsel: %d Pausen, %d s Arbeit", l.BreaksToday, l.WorkSecsToday)
	}

	*clk = breakStart.AddDate(0, 0, 1)
	BreakHeartbeat(true)
	if l := breakLogSnapshot(); l.BreaksToday != 0 || l.BreakSecsToday != 0 || l.WorkSecsToday != 0 {
		t.Fatalf("Tageszähler nicht zurückgesetzt: %+v", l)
	}

}

func TestCorruptFileYieldsEmptyLog(t *testing.T) {
	p := useTempBreaks(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{das ist kein json"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeNow(t, breakStart)

	if c := GetBreakConfig(); c != DefaultBreakConfig() {
		t.Fatalf("beschädigte Datei muss die Vorgaben liefern: %+v", c)
	}
	a := BreakHeartbeat(true)
	if !a.Enabled || a.Level != BreakLevelNone || breakLogSnapshot().BreaksToday != 0 || a.WorkedSecs != 0 {
		t.Fatalf("beschädigte Datei liefert keinen leeren Log: %+v", a)
	}
}

func TestMissingFileYieldsEmptyLog(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)
	a := BreakHeartbeat(false)
	if !a.Enabled || a.Level != BreakLevelNone || a.WorkedSecs != 0 {
		t.Fatalf("fehlende Datei: %+v", a)
	}
}

func TestConfigDefaultsAndNormalisierung(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)

	if c := GetBreakConfig(); c != DefaultBreakConfig() {
		t.Fatalf("ohne Datei: %+v", c)
	}
	if err := SetBreakConfig(BreakConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if c := GetBreakConfig(); c != DefaultBreakConfig() {
		t.Fatalf("Nullwerte müssen zu Vorgaben werden: %+v", c)
	}

	if err := SetBreakConfig(BreakConfig{Enabled: true, HintAfter: 90, DueAfter: 20, OverdueAfter: 30, MinBreak: 5, IdleResets: 7, SnoozeMins: 3}); err != nil {
		t.Fatal(err)
	}
	c := GetBreakConfig()
	if c.HintAfter != 40 || c.DueAfter != 55 || c.OverdueAfter != 80 {
		t.Fatalf("verletzte Reihenfolge nicht korrigiert: %+v", c)
	}
	if c.MinBreak != 5 || c.IdleResets != 7 || c.SnoozeMins != 3 {
		t.Fatalf("gültige Werte wurden überschrieben: %+v", c)
	}
}

func TestConfigÜberlebtNeuladen(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)
	if err := SetBreakConfig(BreakConfig{Enabled: false, HintAfter: 30, DueAfter: 45, OverdueAfter: 60, MinBreak: 3, IdleResets: 8, SnoozeMins: 5}); err != nil {
		t.Fatal(err)
	}
	resetBreakState()

	c := GetBreakConfig()
	if c.Enabled || c.HintAfter != 30 || c.IdleResets != 8 {
		t.Fatalf("Konfiguration nicht gespeichert: %+v", c)
	}
	a := BreakHeartbeat(true)
	if a.Enabled || a.Message != "" {
		t.Fatalf("abgeschaltet muss stumm bleiben: %+v", a)
	}
}

func TestCustomThresholds(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	if err := SetBreakConfig(BreakConfig{Enabled: true, HintAfter: 10, DueAfter: 20, OverdueAfter: 30, MinBreak: 2, IdleResets: 3, SnoozeMins: 5}); err != nil {
		t.Fatal(err)
	}
	a := workMinutes(t, clk, breakStart, 20)
	if a.Level != BreakLevelDue {
		t.Fatalf("eigene Schwellen greifen nicht: %s", a.Level)
	}
	*clk = breakStart.Add(23 * time.Minute)
	if a = BreakHeartbeat(false); a.Level != BreakLevelResting {
		t.Fatalf("eigenes IdleResets greift nicht: %s", a.Level)
	}
	*clk = breakStart.Add(25 * time.Minute)
	a = BreakHeartbeat(true)
	if breakLogSnapshot().BreaksToday != 1 || breakLogSnapshot().BreakSecsToday != 5*60 {
		t.Fatalf("Auto-Pause nach eigenen Regeln: %d / %d s", breakLogSnapshot().BreaksToday, breakLogSnapshot().BreakSecsToday)
	}
}

func TestKurzeRuheZaehltNichtAlsPause(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	if err := SetBreakConfig(BreakConfig{Enabled: true, HintAfter: 10, DueAfter: 20, OverdueAfter: 30, MinBreak: 9, IdleResets: 3, SnoozeMins: 5}); err != nil {
		t.Fatal(err)
	}
	workMinutes(t, clk, breakStart, 20)

	*clk = breakStart.Add(24 * time.Minute)
	BreakHeartbeat(false)
	*clk = breakStart.Add(26 * time.Minute)
	BreakHeartbeat(true)
	if breakLogSnapshot().BreaksToday != 0 {
		t.Fatalf("6 Minuten Ruhe unter MinBreak=9 dürfen nicht als Pause zählen: %d", breakLogSnapshot().BreaksToday)
	}
	if breakLogSnapshot().WorkSecsToday != 20*60 {
		t.Fatalf("Arbeitsblock trotzdem verbuchen: %d s", breakLogSnapshot().WorkSecsToday)
	}
}

func TestStatusSchreibtNichtBeiJedemAufruf(t *testing.T) {
	p := useTempBreaks(t)
	clk := fakeNow(t, breakStart)

	*clk = breakStart
	BreakHeartbeat(true)
	f := readBreakFile(t, p)
	if !f.Log.WorkStartedAt.Equal(breakStart) {
		t.Fatalf("Beginn des Arbeitsblocks nicht gespeichert: %v", f.Log.WorkStartedAt)
	}

	for _, d := range []time.Duration{2 * time.Second, 5 * time.Second, 9 * time.Second} {
		*clk = breakStart.Add(d)
		BreakHeartbeat(true)
	}
	f = readBreakFile(t, p)
	if !f.Log.LastActiveAt.Equal(breakStart) {
		t.Fatalf("Datei wurde ohne wesentliche Änderung neu geschrieben: %v", f.Log.LastActiveAt)
	}

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("Zwischendatei übrig: %s", e.Name())
		}
	}
}

func TestNachrichtenPassenZurLage(t *testing.T) {
	useTempBreaks(t)
	clk := fakeNow(t, breakStart)
	st := &State{Agents: []Agent{{Name: "hera"}, {Name: "atlas"}}}
	running := map[string]AgentStatus{"hera": StatusRunning, "atlas": StatusRunning}
	waiting := map[string]AgentStatus{"hera": StatusRunning, "atlas": StatusBlocked}

	*clk = breakStart
	BreakHeartbeat(true)
	for m := 1; m <= 58; m++ {
		*clk = breakStart.Add(time.Duration(m) * time.Minute)
		BreakHeartbeat(true)
	}

	good := BreakStatus(st, running, nil)
	if good.Level != BreakLevelDue || !good.GoodMoment {
		t.Fatalf("Ausgangslage falsch: %s / %v", good.Level, good.GoodMoment)
	}
	if !strings.Contains(good.Message, "58") || !strings.Contains(good.Message, "Agents") {
		t.Fatalf("Meldung bei gutem Moment: %q", good.Message)
	}

	busy := BreakStatus(st, waiting, nil)
	if busy.GoodMoment {
		t.Fatal("wartende Session verhindert den guten Moment nicht")
	}
	if !strings.Contains(busy.Message, "wartende Session") {
		t.Fatalf("Meldung bei wartender Session: %q", busy.Message)
	}
}

// Der Alltagsfall: mehrere Agents rechnen, daneben stehen eine fertige Session
// und offene Terminals. Das darf einen Pausenvorschlag nicht blockieren.
func TestGoodMomentIgnoresIdleAndTerminals(t *testing.T) {
	cases := []struct {
		name     string
		statuses map[string]AgentStatus
		want     bool
		waiting  int
	}{
		{
			name: "rechnende Agents neben idle Session und Terminals",
			statuses: map[string]AgentStatus{
				"a": StatusRunning, "b": StatusRunning,
				"c": StatusIdle, "t1": StatusTerm, "t2": StatusTerm,
			},
			want: true, waiting: 0,
		},
		{
			name:     "eine Session wartet wirklich auf Eingabe",
			statuses: map[string]AgentStatus{"a": StatusRunning, "b": StatusBlocked},
			want:     false, waiting: 1,
		},
		{
			name:     "nichts rechnet, nur idle und Terminal",
			statuses: map[string]AgentStatus{"c": StatusIdle, "t1": StatusTerm},
			want:     false, waiting: 0,
		},
		{
			name:     "Hintergrund-Agents zählen als beschäftigt",
			statuses: map[string]AgentStatus{"a": StatusAgents, "t1": StatusTerm},
			want:     true, waiting: 0,
		},
		{
			name:     "gar keine Sessions",
			statuses: map[string]AgentStatus{},
			want:     false, waiting: 0,
		},
	}
	for _, c := range cases {
		busy, waiting, _ := countSessions(nil, c.statuses)
		got := busy > 0 && waiting == 0
		if got != c.want {
			t.Errorf("%s: GoodMoment %v, erwartet %v (busy=%d waiting=%d)", c.name, got, c.want, busy, waiting)
		}
		if waiting != c.waiting {
			t.Errorf("%s: Waiting %d, erwartet %d", c.name, waiting, c.waiting)
		}
	}
}

// Tippen in einem Terminal oder einer idle Session ist Anwesenheit, das
// Rechnen der Agents nicht — die beiden Begriffe dürfen nicht zusammenfallen.
func TestInputSignalIsWiderThanNeedsUser(t *testing.T) {
	for _, st := range []AgentStatus{StatusIdle, StatusTerm, StatusExited} {
		if !acceptsInput(st) {
			t.Errorf("%s muss als Eingabesignal zählen", st.Label())
		}
		if needsUser(st) {
			t.Errorf("%s darf keine Handlung verlangen", st.Label())
		}
	}
	if !needsUser(StatusBlocked) || !acceptsInput(StatusBlocked) {
		t.Error("eine blockierte Session ist beides")
	}
	for _, st := range []AgentStatus{StatusRunning, StatusAgents, StatusShell} {
		if acceptsInput(st) {
			t.Errorf("%s darf kein Eingabesignal sein", st.Label())
		}
	}
}

func TestBreakMinsGehoertZurKonfiguration(t *testing.T) {
	useTempBreaks(t)
	fakeNow(t, breakStart)

	if got := GetBreakConfig().BreakMins; got != 5 {
		t.Fatalf("Vorgabe für die Pausenlänge ist %d, erwartet 5", got)
	}
	c := GetBreakConfig()
	c.BreakMins = 12
	if err := SetBreakConfig(c); err != nil {
		t.Fatal(err)
	}
	resetBreakState()
	if got := GetBreakConfig().BreakMins; got != 12 {
		t.Fatalf("Pausenlänge überlebt das Neuladen nicht: %d", got)
	}

	for _, bad := range []int{0, -3, 500} {
		c.BreakMins = bad
		if err := SetBreakConfig(c); err != nil {
			t.Fatal(err)
		}
		resetBreakState()
		if got := GetBreakConfig().BreakMins; got != 5 {
			t.Fatalf("unsinnige Länge %d wurde nicht auf die Vorgabe zurückgesetzt: %d", bad, got)
		}
	}
}
