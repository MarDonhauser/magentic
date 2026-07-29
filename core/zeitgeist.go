package core

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ZgProject struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Client string  `json:"client"`
	Rate   float64 `json:"rate"`
	Color  string  `json:"color"`
}

type ZgPause struct {
	Start string  `json:"start"`
	End   *string `json:"end"`
}

type ZgCurrent struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Start     string    `json:"start"`
	Pauses    []ZgPause `json:"pauses"`
	State     string    `json:"state"`
	Rate      float64   `json:"rate"`
}

type zgSession struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Start       string    `json:"start"`
	End         string    `json:"end"`
	Pauses      []ZgPause `json:"pauses"`
	Rate        float64   `json:"rate"`
	DurationSec int       `json:"durationSec"`
	Earnings    float64   `json:"earnings"`
	Summary     *string   `json:"summary"`
}

type zgSessionLite struct {
	ProjectID   string  `json:"projectId"`
	End         string  `json:"end"`
	DurationSec float64 `json:"durationSec"`
	Earnings    float64 `json:"earnings"`
}

// Projekte und Sessions bleiben RawMessage, damit Felder, die magentic nicht
// kennt, beim Zurückschreiben nicht verloren gehen.
type zgFile struct {
	Projects []json.RawMessage `json:"projects"`
	Sessions []json.RawMessage `json:"sessions"`
	Current  *ZgCurrent        `json:"current"`
}

type ZgInfo struct {
	Exists      bool        `json:"exists"`
	Active      bool        `json:"active"`
	State       string      `json:"state"`
	Project     string      `json:"project"`
	Rate        float64     `json:"rate"`
	Start       string      `json:"start"`
	ElapsedSec  int         `json:"elapsedSec"`
	Earnings    float64     `json:"earnings"`
	TodaySec    int         `json:"todaySec"`
	TodayCash   float64     `json:"todayCash"`
	LastProject string      `json:"lastProject"`
	Projects    []ZgProject `json:"projects"`
}

type ZgStopped struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	DurationSec int     `json:"durationSec"`
	Earnings    float64 `json:"earnings"`
}

func ZeitgeistFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".zeitgeist", "data.json")
}

func zgLoad() (*zgFile, error) {
	data, err := os.ReadFile(ZeitgeistFile())
	if err != nil {
		return nil, err
	}
	f := &zgFile{}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, err
	}
	return f, nil
}

func zgSave(f *zgFile) error {
	if f.Projects == nil {
		f.Projects = []json.RawMessage{}
	}
	if f.Sessions == nil {
		f.Sessions = []json.RawMessage{}
	}
	p := ZeitgeistFile()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func zgISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000") + "Z"
}

func zgParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func zgNewID() string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	id := strconv.FormatInt(time.Now().UnixMilli(), 36)
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return id + string(b)
}

func zgProjects(f *zgFile) []ZgProject {
	out := make([]ZgProject, 0, len(f.Projects))
	for _, raw := range f.Projects {
		var p ZgProject
		if json.Unmarshal(raw, &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

func zgFindProject(ps []ZgProject, ref string) *ZgProject {
	if ref == "" {
		return nil
	}
	for i := range ps {
		if ps[i].ID == ref {
			return &ps[i]
		}
	}
	for i := range ps {
		if strings.EqualFold(ps[i].Name, ref) {
			return &ps[i]
		}
	}
	low := strings.ToLower(ref)
	for i := range ps {
		if strings.Contains(strings.ToLower(ps[i].Name), low) {
			return &ps[i]
		}
	}
	return nil
}

func zgElapsedSec(c *ZgCurrent, at time.Time) int {
	var paused time.Duration
	for _, p := range c.Pauses {
		start := zgParseTime(p.Start)
		if start.IsZero() {
			continue
		}
		end := at
		if p.End != nil {
			end = zgParseTime(*p.End)
		}
		paused += end.Sub(start)
	}
	d := at.Sub(zgParseTime(c.Start)) - paused
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}

func zgSessionDuration(startISO, endISO string, pauses []ZgPause) int {
	var paused time.Duration
	for _, p := range pauses {
		if p.End == nil {
			continue
		}
		ps, pe := zgParseTime(p.Start), zgParseTime(*p.End)
		if !ps.IsZero() && !pe.IsZero() {
			paused += pe.Sub(ps)
		}
	}
	d := zgParseTime(endISO).Sub(zgParseTime(startISO)) - paused
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}

func zgEarnings(sec int, rate float64) float64 {
	return math.Round(float64(sec)/3600*rate*100) / 100
}

func ZeitgeistInfo() ZgInfo {
	info := ZgInfo{}
	f, err := zgLoad()
	if err != nil {
		return info
	}
	info.Exists = true
	ps := zgProjects(f)
	info.Projects = ps
	byID := map[string]string{}
	for _, p := range ps {
		byID[p.ID] = p.Name
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var lastEnd time.Time
	for _, raw := range f.Sessions {
		var s zgSessionLite
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		end := zgParseTime(s.End)
		if end.After(lastEnd) {
			lastEnd = end
			info.LastProject = byID[s.ProjectID]
		}
		if !end.Before(midnight) {
			info.TodaySec += int(s.DurationSec)
			info.TodayCash += s.Earnings
		}
	}
	if c := f.Current; c != nil {
		info.Active = true
		info.State = c.State
		info.Project = byID[c.ProjectID]
		info.Rate = c.Rate
		info.Start = c.Start
		info.ElapsedSec = zgElapsedSec(c, now)
		info.Earnings = zgEarnings(info.ElapsedSec, c.Rate)
		info.TodaySec += info.ElapsedSec
		info.TodayCash += info.Earnings
	}
	info.TodayCash = math.Round(info.TodayCash*100) / 100
	return info
}

func ZeitgeistStart(ref string) (ZgProject, error) {
	var zero ZgProject
	f, err := zgLoad()
	if err != nil {
		return zero, fmt.Errorf("Zeitgeist-Daten nicht lesbar: %w", err)
	}
	if f.Current != nil {
		return zero, fmt.Errorf("es läuft bereits eine Zeitgeist-Session")
	}
	p := zgFindProject(zgProjects(f), strings.TrimSpace(ref))
	if p == nil {
		return zero, fmt.Errorf("Zeitgeist-Projekt nicht gefunden: %s", ref)
	}
	f.Current = &ZgCurrent{
		ID:        zgNewID(),
		ProjectID: p.ID,
		Start:     zgISO(time.Now()),
		Pauses:    []ZgPause{},
		State:     "running",
		Rate:      p.Rate,
	}
	return *p, zgSave(f)
}

func ZeitgeistPause() error {
	f, err := zgLoad()
	if err != nil {
		return err
	}
	if f.Current == nil || f.Current.State != "running" {
		return fmt.Errorf("keine laufende Zeitgeist-Session")
	}
	f.Current.Pauses = append(f.Current.Pauses, ZgPause{Start: zgISO(time.Now())})
	f.Current.State = "paused"
	return zgSave(f)
}

func ZeitgeistResume() error {
	f, err := zgLoad()
	if err != nil {
		return err
	}
	if f.Current == nil || f.Current.State != "paused" {
		return fmt.Errorf("keine pausierte Zeitgeist-Session")
	}
	if n := len(f.Current.Pauses); n > 0 && f.Current.Pauses[n-1].End == nil {
		end := zgISO(time.Now())
		f.Current.Pauses[n-1].End = &end
	}
	f.Current.State = "running"
	return zgSave(f)
}

func ZeitgeistStop(note string) (ZgStopped, error) {
	var zero ZgStopped
	f, err := zgLoad()
	if err != nil {
		return zero, err
	}
	c := f.Current
	if c == nil {
		return zero, fmt.Errorf("keine aktive Zeitgeist-Session")
	}
	nowISO := zgISO(time.Now())
	if c.State == "paused" {
		if n := len(c.Pauses); n > 0 && c.Pauses[n-1].End == nil {
			c.Pauses[n-1].End = &nowISO
		}
	}
	sec := zgSessionDuration(c.Start, nowISO, c.Pauses)
	s := zgSession{
		ID:          c.ID,
		ProjectID:   c.ProjectID,
		Start:       c.Start,
		End:         nowISO,
		Pauses:      c.Pauses,
		Rate:        c.Rate,
		DurationSec: sec,
		Earnings:    zgEarnings(sec, c.Rate),
	}
	if note = strings.TrimSpace(note); note != "" {
		s.Summary = &note
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return zero, err
	}
	f.Sessions = append(f.Sessions, raw)
	f.Current = nil
	if err := zgSave(f); err != nil {
		return zero, err
	}
	name := ""
	for _, p := range zgProjects(f) {
		if p.ID == s.ProjectID {
			name = p.Name
		}
	}
	return ZgStopped{ID: s.ID, Project: name, DurationSec: sec, Earnings: s.Earnings}, nil
}

func FormatEuro(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1) + " €"
}

func FormatDurShort(sec int) string {
	min := (sec + 30) / 60
	if min >= 60 {
		return fmt.Sprintf("%dh %dm", min/60, min%60)
	}
	return fmt.Sprintf("%dm", min)
}
