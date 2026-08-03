package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var now = time.Now

const (
	BreakKindTaken   = "taken"
	BreakKindAuto    = "auto"
	BreakKindSkipped = "skipped"
)

const (
	BreakLevelNone    = "none"
	BreakLevelHint    = "hint"
	BreakLevelDue     = "due"
	BreakLevelOverdue = "overdue"
	BreakLevelResting = "resting"
)

const (
	breakHistoryMax = 200
	breakWriteEvery = 15 * time.Second
	// Wer abends den Rechner stehen lässt, kommt am nächsten Morgen sonst mit
	// einer zwölfstündigen "Pause" in der Statistik zurück.
	maxAutoBreakSecs = 2 * 3600
)

type BreakConfig struct {
	Enabled      bool `json:"enabled"`
	HintAfter    int  `json:"hintAfter"`
	DueAfter     int  `json:"dueAfter"`
	OverdueAfter int  `json:"overdueAfter"`
	MinBreak     int  `json:"minBreak"`
	IdleResets   int  `json:"idleResets"`
	SnoozeMins   int  `json:"snoozeMins"`
	BreakMins    int  `json:"breakMins"`
}

type BreakEntry struct {
	At       time.Time `json:"at"`
	Secs     int       `json:"secs"`
	WorkSecs int       `json:"workSecs"`
	Kind     string    `json:"kind"`
}

type BreakLog struct {
	WorkStartedAt   time.Time    `json:"workStartedAt,omitzero"`
	LastActiveAt    time.Time    `json:"lastActiveAt,omitzero"`
	LastBreakAt     time.Time    `json:"lastBreakAt,omitzero"`
	LastBreakSecs   int          `json:"lastBreakSecs"`
	SnoozeUntil     time.Time    `json:"snoozeUntil,omitzero"`
	Today           string       `json:"today"`
	BreaksToday     int          `json:"breaksToday"`
	BreakSecsToday  int          `json:"breakSecsToday"`
	WorkSecsToday   int          `json:"workSecsToday"`
	RestingSince    time.Time    `json:"restingSince,omitzero"`
	RestingWorkSecs int          `json:"restingWorkSecs,omitempty"`
	BreakRunning    bool         `json:"breakRunning,omitempty"`
	History         []BreakEntry `json:"history,omitempty"`
}

type BreakAdvice struct {
	Enabled     bool   `json:"enabled"`
	Level       string `json:"level"`
	WorkedSecs  int    `json:"workedSecs"`
	RestingSecs int    `json:"restingSecs"`
	GoodMoment  bool   `json:"goodMoment"`
	Waiting     int    `json:"waiting"`
	Busy        int    `json:"busy"`
	Message     string `json:"message"`
	Snoozed     bool   `json:"snoozed"`
	NextDueSecs int    `json:"nextDueSecs"`
}

type breakFile struct {
	Config *BreakConfig `json:"config,omitempty"`
	Log    BreakLog     `json:"log"`
}

var (
	breakMu        sync.Mutex
	breakCache     *breakFile
	breakCachePath string
	breakPending   bool
	breakLastWrite time.Time
	breakSeen      = map[string]time.Time{}
)

func DefaultBreakConfig() BreakConfig {
	return BreakConfig{
		Enabled:      true,
		HintAfter:    40,
		DueAfter:     55,
		OverdueAfter: 80,
		MinBreak:     4,
		IdleResets:   6,
		SnoozeMins:   10,
		BreakMins:    5,
	}
}

func normalizeBreakConfig(c BreakConfig) BreakConfig {
	d := DefaultBreakConfig()
	if c.HintAfter <= 0 {
		c.HintAfter = d.HintAfter
	}
	if c.DueAfter <= 0 {
		c.DueAfter = d.DueAfter
	}
	if c.OverdueAfter <= 0 {
		c.OverdueAfter = d.OverdueAfter
	}
	if c.HintAfter >= c.DueAfter || c.DueAfter >= c.OverdueAfter {
		c.HintAfter, c.DueAfter, c.OverdueAfter = d.HintAfter, d.DueAfter, d.OverdueAfter
	}
	if c.MinBreak <= 0 {
		c.MinBreak = d.MinBreak
	}
	if c.IdleResets <= 0 {
		c.IdleResets = d.IdleResets
	}
	if c.SnoozeMins <= 0 {
		c.SnoozeMins = d.SnoozeMins
	}
	if c.BreakMins <= 0 || c.BreakMins > 120 {
		c.BreakMins = d.BreakMins
	}
	return c
}

func (f *breakFile) config() BreakConfig {
	if f.Config == nil {
		return DefaultBreakConfig()
	}
	return *f.Config
}

func BreaksPath() string {
	return filepath.Join(filepath.Dir(StatePath()), "breaks.json")
}

func loadBreakFile() *breakFile {
	p := BreaksPath()
	if breakCache != nil && breakCachePath == p {
		return breakCache
	}
	breakCachePath = p
	breakPending = false
	breakLastWrite = time.Time{}
	breakSeen = map[string]time.Time{}

	f := &breakFile{}
	if data, err := os.ReadFile(p); err == nil {
		var parsed breakFile
		if json.Unmarshal(data, &parsed) == nil {
			f = &parsed
		}
	}
	if f.Config != nil {
		c := normalizeBreakConfig(*f.Config)
		f.Config = &c
	}
	breakCache = f
	return f
}

func saveBreakFile(f *breakFile, t time.Time) {
	breakPending = false
	breakLastWrite = t
	p := BreaksPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
	}
}

func resetBreakState() {
	breakMu.Lock()
	defer breakMu.Unlock()
	breakCache = nil
	breakCachePath = ""
	breakPending = false
	breakLastWrite = time.Time{}
	breakSeen = map[string]time.Time{}
}

func dayKey(t time.Time) string {
	return t.In(time.Local).Format(statsDateLayout)
}

func rolloverDay(f *breakFile, t time.Time) bool {
	k := dayKey(t)
	if f.Log.Today == k {
		return false
	}
	f.Log.Today = k
	f.Log.BreaksToday = 0
	f.Log.BreakSecsToday = 0
	f.Log.WorkSecsToday = 0
	return true
}

// Sessions, in denen Ausgabe nur entsteht, wenn jemand tippt — das Signal für
// Anwesenheit.
func acceptsInput(st AgentStatus) bool {
	switch st {
	case StatusBlocked, StatusIdle, StatusExited, StatusTerm:
		return true
	}
	return false
}

// Nur eine blockierte Session verlangt wirklich eine Antwort. Eine idle Session
// oder ein offenes Terminal darf einen Pausenvorschlag nicht verhindern —
// sonst gäbe es nie einen guten Moment.
func needsUser(st AgentStatus) bool {
	return st == StatusBlocked
}

func isBusy(st AgentStatus) bool {
	switch st {
	case StatusRunning, StatusAgents, StatusShell:
		return true
	}
	return false
}

func countSessions(s *State, statuses map[string]AgentStatus) (busy, waiting, alive int) {
	count := func(st AgentStatus) {
		if needsUser(st) {
			waiting++
		}
		switch {
		case isBusy(st):
			busy++
			alive++
		case acceptsInput(st):
			alive++
		}
	}
	if s != nil {
		for _, a := range s.Agents {
			if st, ok := statuses[a.Name]; ok {
				count(st)
			}
		}
		return
	}
	for _, st := range statuses {
		count(st)
	}
	return
}

// Neue tmux-Aktivität in einer Session, die auf Eingabe wartet, heißt: er hat
// getippt. Rechnende Agents schreiben zwar auch ständig in den Pane, das ist
// aber keine Anwesenheit.
func trackSessionActivity(statuses map[string]AgentStatus, activity map[string]time.Time) bool {
	active := false
	for name, at := range activity {
		prev, known := breakSeen[name]
		breakSeen[name] = at
		if known && at.After(prev) && acceptsInput(statuses[name]) {
			active = true
		}
	}
	for name := range breakSeen {
		if _, ok := activity[name]; !ok {
			delete(breakSeen, name)
		}
	}
	return active
}

func closeWorkBlock(f *breakFile, endedAt time.Time) {
	secs := int(endedAt.Sub(f.Log.WorkStartedAt).Seconds())
	if secs < 0 {
		secs = 0
	}
	f.Log.WorkSecsToday += secs
	f.Log.RestingWorkSecs = secs
	f.Log.RestingSince = endedAt
	f.Log.WorkStartedAt = time.Time{}
}

func bookRest(f *breakFile, t time.Time, cfg BreakConfig, kind string) {
	secs := int(t.Sub(f.Log.RestingSince).Seconds())
	if secs < 0 {
		secs = 0
	}
	if kind == BreakKindAuto && secs > maxAutoBreakSecs {
		secs = maxAutoBreakSecs
	}
	if kind == BreakKindAuto && secs < cfg.MinBreak*60 {
		kind = BreakKindSkipped
	}
	e := BreakEntry{At: f.Log.RestingSince, Secs: secs, WorkSecs: f.Log.RestingWorkSecs, Kind: kind}
	f.Log.History = append(f.Log.History, e)
	if len(f.Log.History) > breakHistoryMax {
		f.Log.History = f.Log.History[len(f.Log.History)-breakHistoryMax:]
	}
	if kind != BreakKindSkipped {
		f.Log.LastBreakAt = e.At
		f.Log.LastBreakSecs = secs
		if dayKey(e.At) == dayKey(t) {
			f.Log.BreaksToday++
			f.Log.BreakSecsToday += secs
		}
	}
	f.Log.RestingSince = time.Time{}
	f.Log.RestingWorkSecs = 0
	f.Log.BreakRunning = false
}

func openWorkSecs(l *BreakLog, t time.Time) int {
	if l.WorkStartedAt.IsZero() {
		return 0
	}
	secs := int(t.Sub(l.WorkStartedAt).Seconds())
	if secs < 0 {
		return 0
	}
	return secs
}

func breakLevel(workedSecs int, cfg BreakConfig) string {
	switch m := workedSecs / 60; {
	case m >= cfg.OverdueAfter:
		return BreakLevelOverdue
	case m >= cfg.DueAfter:
		return BreakLevelDue
	case m >= cfg.HintAfter:
		return BreakLevelHint
	}
	return BreakLevelNone
}

func BreakStatus(s *State, statuses map[string]AgentStatus, activity map[string]time.Time) BreakAdvice {
	return evaluateBreak(false, s, statuses, activity)
}

func BreakHeartbeat(active bool) BreakAdvice {
	return evaluateBreak(active, nil, nil, nil)
}

func evaluateBreak(userActive bool, s *State, statuses map[string]AgentStatus, activity map[string]time.Time) BreakAdvice {
	breakMu.Lock()
	defer breakMu.Unlock()

	f := loadBreakFile()
	cfg := f.config()
	t := now()

	if trackSessionActivity(statuses, activity) {
		userActive = true
	}
	if !cfg.Enabled {
		return BreakAdvice{Level: BreakLevelNone}
	}

	significant := rolloverDay(f, t)
	idle := time.Duration(cfg.IdleResets) * time.Minute

	if !f.Log.BreakRunning {
		if !f.Log.WorkStartedAt.IsZero() && !f.Log.LastActiveAt.IsZero() && t.Sub(f.Log.LastActiveAt) >= idle {
			closeWorkBlock(f, f.Log.LastActiveAt)
			significant = true
		}
		if userActive {
			if !f.Log.RestingSince.IsZero() {
				bookRest(f, t, cfg, BreakKindAuto)
				significant = true
			}
			if f.Log.WorkStartedAt.IsZero() {
				f.Log.WorkStartedAt = t
				significant = true
			}
			f.Log.LastActiveAt = t
		}
	}

	if significant {
		breakPending = true
	}
	if breakPending && t.Sub(breakLastWrite) >= breakWriteEvery {
		saveBreakFile(f, t)
	}

	busy, waiting, _ := countSessions(s, statuses)
	worked := openWorkSecs(&f.Log, t)

	resting := 0
	switch {
	case !f.Log.RestingSince.IsZero():
		resting = int(t.Sub(f.Log.RestingSince).Seconds())
	case !userActive && !f.Log.LastActiveAt.IsZero():
		resting = int(t.Sub(f.Log.LastActiveAt).Seconds())
	}
	if resting < 0 {
		resting = 0
	}

	level := breakLevel(worked, cfg)
	if !f.Log.RestingSince.IsZero() {
		level = BreakLevelResting
	}
	snoozed := t.Before(f.Log.SnoozeUntil)
	if snoozed && (level == BreakLevelDue || level == BreakLevelOverdue) {
		level = BreakLevelHint
	}

	nextDue := cfg.DueAfter*60 - worked
	if nextDue < 0 {
		nextDue = 0
	}
	if snoozed {
		if rest := int(f.Log.SnoozeUntil.Sub(t).Seconds()); rest > nextDue {
			nextDue = rest
		}
	}

	adv := BreakAdvice{
		Enabled:     true,
		Level:       level,
		WorkedSecs:  worked,
		RestingSecs: resting,
		GoodMoment:  busy > 0 && waiting == 0,
		Waiting:     waiting,
		Busy:        busy,
		Snoozed:     snoozed,
		NextDueSecs: nextDue,
	}
	adv.Message = breakMessage(adv)
	return adv
}

func waitingPhrase(n int) string {
	if n == 1 {
		return "die wartende Session"
	}
	return fmt.Sprintf("die %d wartenden Sessions", n)
}

func breakMessage(a BreakAdvice) string {
	mins := a.WorkedSecs / 60
	if a.Level == BreakLevelResting {
		r := a.RestingSecs / 60
		if r < 1 {
			return "Pause läuft — lass dir Zeit."
		}
		return fmt.Sprintf("Pause läuft seit %d Minuten.", r)
	}
	if a.Snoozed {
		return fmt.Sprintf("Seit %d Minuten dran — der Hinweis ist kurz stumm.", mins)
	}
	switch a.Level {
	case BreakLevelHint:
		if a.GoodMoment {
			return fmt.Sprintf("Seit %d Minuten dran, und gerade rechnen alle Agents — ein guter Moment, kurz aufzustehen.", mins)
		}
		return fmt.Sprintf("Seit %d Minuten dran. Wenn es gerade passt, steh kurz auf.", mins)
	case BreakLevelDue:
		if a.GoodMoment {
			return fmt.Sprintf("Seit %d Minuten dran, und gerade rechnen alle Agents — guter Moment für ein paar Minuten Abstand.", mins)
		}
		if a.Waiting > 0 {
			return fmt.Sprintf("Seit %d Minuten dran. Wenn du %s beantwortet hast, mach kurz Pause.", mins, waitingPhrase(a.Waiting))
		}
		return fmt.Sprintf("Seit %d Minuten dran. Guter Zeitpunkt für ein paar Minuten Abstand.", mins)
	case BreakLevelOverdue:
		if a.GoodMoment {
			return fmt.Sprintf("Seit %d Minuten am Stück, und alle Agents rechnen — jetzt passt eine Pause richtig gut.", mins)
		}
		if a.Waiting > 0 {
			return fmt.Sprintf("Seit %d Minuten am Stück. Beantworte %s und nimm dir danach ein paar Minuten.", mins, waitingPhrase(a.Waiting))
		}
		return fmt.Sprintf("Seit %d Minuten am Stück. Nimm dir jetzt ein paar Minuten.", mins)
	}
	if mins < 1 {
		return "Frisch dabei."
	}
	return fmt.Sprintf("Seit %d Minuten dran, alles entspannt.", mins)
}

func TakeBreak() error {
	breakMu.Lock()
	defer breakMu.Unlock()

	f := loadBreakFile()
	t := now()
	rolloverDay(f, t)
	if f.Log.BreakRunning {
		saveBreakFile(f, t)
		return nil
	}
	if !f.Log.WorkStartedAt.IsZero() {
		closeWorkBlock(f, t)
	}
	if f.Log.RestingSince.IsZero() {
		f.Log.RestingSince = t
	}
	f.Log.BreakRunning = true
	f.Log.SnoozeUntil = time.Time{}
	saveBreakFile(f, t)
	return nil
}

func EndBreak() error {
	breakMu.Lock()
	defer breakMu.Unlock()

	f := loadBreakFile()
	t := now()
	cfg := f.config()
	if !f.Log.BreakRunning {
		return nil
	}
	rolloverDay(f, t)
	bookRest(f, t, cfg, BreakKindTaken)
	f.Log.WorkStartedAt = t
	f.Log.LastActiveAt = t
	f.Log.SnoozeUntil = time.Time{}
	saveBreakFile(f, t)
	return nil
}

func SnoozeBreak() error {
	breakMu.Lock()
	defer breakMu.Unlock()

	f := loadBreakFile()
	t := now()
	f.Log.SnoozeUntil = t.Add(time.Duration(f.config().SnoozeMins) * time.Minute)
	saveBreakFile(f, t)
	return nil
}

func GetBreakConfig() BreakConfig {
	breakMu.Lock()
	defer breakMu.Unlock()
	return loadBreakFile().config()
}

func SetBreakConfig(c BreakConfig) error {
	breakMu.Lock()
	defer breakMu.Unlock()

	f := loadBreakFile()
	n := normalizeBreakConfig(c)
	f.Config = &n
	saveBreakFile(f, now())
	return nil
}

// Nur für Tests: die Tageszähler gehören zur Erkennung, nicht ins Advice —
// die Oberfläche soll keine Bilanz anzeigen.
func breakLogSnapshot() BreakLog {
	breakMu.Lock()
	defer breakMu.Unlock()
	return loadBreakFile().Log
}
