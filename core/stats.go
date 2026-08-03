package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type StatsDay struct {
	Date       string  `json:"date"`
	Weekday    string  `json:"weekday"`
	Prompts    int     `json:"prompts"`
	Turns      int     `json:"turns"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Sessions   int     `json:"sessions"`
	Commits    int     `json:"commits"`
}

type StatsProject struct {
	Name     string  `json:"name"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
	Prompts  int     `json:"prompts"`
	Sessions int     `json:"sessions"`
	Commits  int     `json:"commits"`
	Active   int     `json:"active"`
}

type StatsModel struct {
	Model      string  `json:"model"`
	Turns      int     `json:"turns"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
}

type StatsTotals struct {
	Days       int     `json:"days"`
	Prompts    int     `json:"prompts"`
	Turns      int     `json:"turns"`
	Sessions   int     `json:"sessions"`
	Tokens     int64   `json:"tokens"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Commits    int     `json:"commits"`
	CacheHit   float64 `json:"cacheHit"`
	BusiestDay string  `json:"busiestDay"`
	Streak     int     `json:"streak"`
}

type Stats struct {
	Range    int            `json:"range"`
	Days     []StatsDay     `json:"days"`
	Projects []StatsProject `json:"projects"`
	Models   []StatsModel   `json:"models"`
	Heatmap  [][]int        `json:"heatmap"`
	Hours    []int          `json:"hours"`
	Totals   StatsTotals    `json:"totals"`
	Err      string         `json:"err,omitempty"`
}

const (
	statsDateLayout = "2006-01-02"
	// 2: Slash-Kommando-Zeilen zählen nicht mehr als Prompt.
	statsCacheVersion = 2
	statsOtherProject = "sonstige"
)

var modelPrices = []struct {
	prefix  string
	in, out float64
}{
	{"claude-sonnet-4-6", 3.00, 15.00},
	{"claude-sonnet-4-5", 3.00, 15.00},
	{"claude-haiku-4-5", 1.00, 5.00},
	{"claude-opus-4-8", 5.00, 25.00},
	{"claude-opus-4-7", 5.00, 25.00},
	{"claude-opus-4-6", 5.00, 25.00},
	{"claude-opus-4-5", 5.00, 25.00},
	{"claude-mythos-5", 10.00, 50.00},
	{"claude-sonnet-5", 3.00, 15.00},
	{"claude-fable-5", 10.00, 50.00},
	{"claude-opus-5", 5.00, 25.00},
}

const (
	defaultPriceIn  = 5.00
	defaultPriceOut = 25.00
)

func modelPrice(model string) (float64, float64) {
	for _, p := range modelPrices {
		if strings.HasPrefix(model, p.prefix) {
			return p.in, p.out
		}
	}
	return defaultPriceIn, defaultPriceOut
}

func modelCost(model string, input, output, cacheRead, cacheWrite int64) float64 {
	in, out := modelPrice(model)
	const million = 1_000_000.0
	return float64(input)*in/million +
		float64(output)*out/million +
		float64(cacheWrite)*in*1.25/million +
		float64(cacheRead)*in*0.1/million
}

type statsAgg struct {
	Prompts    int     `json:"p,omitempty"`
	Turns      int     `json:"t,omitempty"`
	Input      int64   `json:"i,omitempty"`
	Output     int64   `json:"o,omitempty"`
	CacheRead  int64   `json:"r,omitempty"`
	CacheWrite int64   `json:"w,omitempty"`
	Cost       float64 `json:"c,omitempty"`
}

func (a *statsAgg) add(b *statsAgg) {
	a.Prompts += b.Prompts
	a.Turns += b.Turns
	a.Input += b.Input
	a.Output += b.Output
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	a.Cost += b.Cost
}

func (a *statsAgg) tokens() int64 {
	return a.Input + a.Output + a.CacheRead + a.CacheWrite
}

type statsFileDay struct {
	Hours    [24]int              `json:"h"`
	Cwds     map[string]*statsAgg `json:"w,omitempty"`
	Models   map[string]*statsAgg `json:"m,omitempty"`
	Sessions map[string][]string  `json:"s,omitempty"`
}

func (d *statsFileDay) cwd(path string) *statsAgg {
	if d.Cwds == nil {
		d.Cwds = map[string]*statsAgg{}
	}
	a := d.Cwds[path]
	if a == nil {
		a = &statsAgg{}
		d.Cwds[path] = a
	}
	return a
}

func (d *statsFileDay) model(name string) *statsAgg {
	if d.Models == nil {
		d.Models = map[string]*statsAgg{}
	}
	a := d.Models[name]
	if a == nil {
		a = &statsAgg{}
		d.Models[name] = a
	}
	return a
}

func (d *statsFileDay) session(cwd, id string) {
	if id == "" {
		return
	}
	if d.Sessions == nil {
		d.Sessions = map[string][]string{}
	}
	for _, s := range d.Sessions[cwd] {
		if s == id {
			return
		}
	}
	d.Sessions[cwd] = append(d.Sessions[cwd], id)
}

type statsCacheFile struct {
	ModTime int64                    `json:"mt"`
	Size    int64                    `json:"size"`
	Days    map[string]*statsFileDay `json:"days,omitempty"`
}

type statsCache struct {
	Version int                        `json:"version"`
	Files   map[string]*statsCacheFile `json:"files"`
}

func statsCachePath() string {
	return filepath.Join(filepath.Dir(StatePath()), "stats-cache.json")
}

func loadStatsCache() *statsCache {
	c := &statsCache{Version: statsCacheVersion, Files: map[string]*statsCacheFile{}}
	data, err := os.ReadFile(statsCachePath())
	if err != nil {
		return c
	}
	var loaded statsCache
	if json.Unmarshal(data, &loaded) != nil || loaded.Version != statsCacheVersion || loaded.Files == nil {
		return c
	}
	return &loaded
}

func saveStatsCache(c *statsCache) {
	p := statsCachePath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	os.Rename(tmp, p)
}

type transcriptLine struct {
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	Cwd           string          `json:"cwd"`
	SessionID     string          `json:"sessionId"`
	IsMeta        bool            `json:"isMeta"`
	IsSidechain   bool            `json:"isSidechain"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Message       struct {
		Model string `json:"model"`
		Usage struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

var (
	keyUsage         = []byte(`"usage"`)
	keyTypeUser      = []byte(`"type":"user"`)
	keyToolUseResult = []byte(`"toolUseResult"`)
)

// Claude Code legt ausgeführte Slash-Kommandos und deren Ausgabe als
// user-Zeilen ab. Das sind keine Prompts — ohne diesen Filter zählt jedes
// `/model` doppelt und jede Kommandoausgabe als weiterer Prompt.
var commandNoise = [][]byte{
	[]byte("<command-name>"),
	[]byte("<command-message>"),
	[]byte("<local-command-stdout>"),
	[]byte("<local-command-stderr>"),
}

func isCommandNoise(line []byte) bool {
	for _, m := range commandNoise {
		if bytes.Contains(line, m) {
			return true
		}
	}
	return false
}

func parseTranscriptFile(path string) map[string]*statsFileDay {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	fallbackSession := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	out := map[string]*statsFileDay{}
	sc := bufio.NewScanner(f)
	// Einzelne Transkript-Zeilen enthalten komplette Dateiinhalte und sprengen den Default-Buffer (64 KB).
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, keyUsage) {
			if !bytes.Contains(line, keyTypeUser) || bytes.Contains(line, keyToolUseResult) {
				continue
			}
		}
		var rec transcriptLine
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		u := rec.Message.Usage
		isTurn := rec.Type == "assistant" && (u.Input|u.Output|u.CacheRead|u.CacheWrite) != 0
		isPrompt := rec.Type == "user" && !rec.IsMeta && !rec.IsSidechain &&
			len(rec.ToolUseResult) == 0 && !isCommandNoise(line)
		if !isTurn && !isPrompt {
			continue
		}
		ts, err := time.Parse(time.RFC3339, rec.Timestamp)
		if err != nil {
			continue
		}
		local := ts.In(time.Local)
		key := local.Format(statsDateLayout)
		day := out[key]
		if day == nil {
			day = &statsFileDay{}
			out[key] = day
		}
		session := rec.SessionID
		if session == "" {
			session = fallbackSession
		}
		day.session(rec.Cwd, session)

		if isPrompt {
			day.Hours[local.Hour()]++
			day.cwd(rec.Cwd).Prompts++
			continue
		}

		cost := modelCost(rec.Message.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
		turn := statsAgg{
			Turns:      1,
			Input:      u.Input,
			Output:     u.Output,
			CacheRead:  u.CacheRead,
			CacheWrite: u.CacheWrite,
			Cost:       cost,
		}
		day.cwd(rec.Cwd).add(&turn)
		model := rec.Message.Model
		if model == "" {
			model = "unbekannt"
		}
		day.model(model).add(&turn)
	}
	return out
}

type statsSlot struct {
	statsAgg
	sessions map[string]bool
}

func newStatsSlot() *statsSlot {
	return &statsSlot{sessions: map[string]bool{}}
}

type statsAcc struct {
	days     map[string]*statsSlot
	projects map[string]*statsSlot
	models   map[string]*statsAgg
	sessions map[string]bool
	hours    [24]int
	heatmap  [7][24]int
}

func newStatsAcc() *statsAcc {
	return &statsAcc{
		days:     map[string]*statsSlot{},
		projects: map[string]*statsSlot{},
		models:   map[string]*statsAgg{},
		sessions: map[string]bool{},
	}
}

func statsSlotFor(m map[string]*statsSlot, key string) *statsSlot {
	s := m[key]
	if s == nil {
		s = newStatsSlot()
		m[key] = s
	}
	return s
}

func (a *statsAcc) merge(fileDays map[string]*statsFileDay, from, to string, resolve func(string) string) {
	for date, fd := range fileDays {
		if date < from || date > to {
			continue
		}
		day := statsSlotFor(a.days, date)
		weekday := statsWeekdayIndex(date)
		for h, n := range fd.Hours {
			if n == 0 {
				continue
			}
			a.hours[h] += n
			if weekday >= 0 {
				a.heatmap[weekday][h] += n
			}
		}
		for cwd, agg := range fd.Cwds {
			day.add(agg)
			project := statsSlotFor(a.projects, resolve(cwd))
			project.add(agg)
			for _, id := range fd.Sessions[cwd] {
				project.sessions[id] = true
				day.sessions[id] = true
				a.sessions[id] = true
			}
		}
		for name, agg := range fd.Models {
			m := a.models[name]
			if m == nil {
				m = &statsAgg{}
				a.models[name] = m
			}
			m.add(agg)
		}
	}
}

func statsWeekdayIndex(date string) int {
	t, err := time.ParseInLocation(statsDateLayout, date, time.Local)
	if err != nil {
		return -1
	}
	return (int(t.Weekday()) + 6) % 7
}

var statsWeekdayNames = [7]string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}

func projectResolver(s *State) func(string) string {
	type entry struct{ path, name string }
	entries := make([]entry, 0, len(s.Projects))
	for _, p := range s.Projects {
		if p.Path == "" {
			continue
		}
		entries = append(entries, entry{filepath.Clean(p.Path), p.Name})
	}
	sort.Slice(entries, func(i, j int) bool { return len(entries[i].path) > len(entries[j].path) })
	cache := map[string]string{}
	var mu sync.Mutex
	return func(cwd string) string {
		if cwd == "" {
			return ""
		}
		mu.Lock()
		defer mu.Unlock()
		if name, ok := cache[cwd]; ok {
			return name
		}
		clean := filepath.Clean(cwd)
		name := ""
		for _, e := range entries {
			if clean == e.path || strings.HasPrefix(clean, e.path+string(filepath.Separator)) {
				name = e.name
				break
			}
		}
		cache[cwd] = name
		return name
	}
}

type statsFileRef struct {
	path    string
	modTime time.Time
	size    int64
}

func transcriptFiles() ([]statsFileRef, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	var refs []statsFileRef
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		refs = append(refs, statsFileRef{path: path, modTime: info.ModTime(), size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

// gitIdentity liefert, als wen dieses Repository committet. In geteilten
// Repositories stammt sonst der Großteil der Commits von anderen und die
// Statistik zählt fremde Arbeit als eigene mit.
func gitIdentity(dir string) (email, name string) {
	if out, err := GitCmd(dir, "config", "user.email"); err == nil {
		email = strings.ToLower(strings.TrimSpace(out))
	}
	if out, err := GitCmd(dir, "config", "user.name"); err == nil {
		name = strings.ToLower(strings.TrimSpace(out))
	}
	return email, name
}

func commitsPerDay(dir, since string) map[string]int {
	email, name := gitIdentity(dir)
	out, err := GitCmd(dir, "log", "--all", "--since="+since, "--format=%ct%x1f%ae%x1f%an")
	if err != nil {
		return nil
	}
	days := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 3 {
			continue
		}
		if !ownCommit(f[1], f[2], email, name) {
			continue
		}
		sec, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		days[time.Unix(sec, 0).In(time.Local).Format(statsDateLayout)]++
	}
	return days
}

func ownCommit(commitEmail, commitName, email, name string) bool {
	if email == "" && name == "" {
		return true
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(commitEmail), email) {
		return true
	}
	return name != "" && strings.EqualFold(strings.TrimSpace(commitName), name)
}

func BuildStats(s *State, days int) Stats {
	if days <= 0 || days > 365 {
		days = 30
	}
	if s == nil {
		loaded, err := LoadState()
		if err != nil || loaded == nil {
			loaded = &State{}
		}
		s = loaded
	}

	st := Stats{Range: days}
	now := time.Now().In(time.Local)
	last := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	first := last.AddDate(0, 0, -(days - 1))
	fromKey, toKey := first.Format(statsDateLayout), last.Format(statsDateLayout)

	acc := newStatsAcc()
	resolve := projectResolver(s)

	commits := map[string]map[string]int{}
	var commitMu sync.Mutex
	var commitWg sync.WaitGroup
	for _, p := range s.Projects {
		if p.Path == "" {
			continue
		}
		commitWg.Add(1)
		go func(p Project) {
			defer commitWg.Done()
			byDay := commitsPerDay(p.Path, fromKey)
			if len(byDay) == 0 {
				return
			}
			commitMu.Lock()
			commits[p.Name] = byDay
			commitMu.Unlock()
		}(p)
	}

	refs, err := transcriptFiles()
	if err != nil {
		st.Err = "Transkripte nicht lesbar: " + err.Error()
	}

	cache := loadStatsCache()
	fresh := &statsCache{Version: statsCacheVersion, Files: make(map[string]*statsCacheFile, len(refs))}
	var mu sync.Mutex

	jobs := make(chan statsFileRef, 64)
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				entry := &statsCacheFile{ModTime: ref.modTime.UnixNano(), Size: ref.size}
				if old := cache.Files[ref.path]; old != nil && old.ModTime == entry.ModTime && old.Size == ref.size {
					entry.Days = old.Days
				} else {
					entry.Days = parseTranscriptFile(ref.path)
				}
				mu.Lock()
				fresh.Files[ref.path] = entry
				acc.merge(entry.Days, fromKey, toKey, resolve)
				mu.Unlock()
			}
		}()
	}
	for _, ref := range refs {
		if ref.modTime.Before(first) {
			if old := cache.Files[ref.path]; old != nil && old.ModTime == ref.modTime.UnixNano() && old.Size == ref.size {
				mu.Lock()
				fresh.Files[ref.path] = old
				mu.Unlock()
			}
			continue
		}
		jobs <- ref
	}
	close(jobs)
	wg.Wait()
	saveStatsCache(fresh)

	commitWg.Wait()

	commitsByDay := map[string]int{}
	commitsByProject := map[string]int{}
	for name, byDay := range commits {
		for date, n := range byDay {
			if date < fromKey || date > toKey {
				continue
			}
			commitsByDay[date] += n
			commitsByProject[name] += n
		}
	}

	active := map[string]int{}
	for _, a := range s.Agents {
		active[a.Project]++
	}

	var totals StatsTotals
	busiestScore := [2]int{-1, -1}
	activeDays := map[string]bool{}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		key := d.Format(statsDateLayout)
		slot := acc.days[key]
		if slot == nil {
			slot = newStatsSlot()
		}
		day := StatsDay{
			Date:       key,
			Weekday:    statsWeekdayNames[(int(d.Weekday())+6)%7],
			Prompts:    slot.Prompts,
			Turns:      slot.Turns,
			Input:      slot.Input,
			Output:     slot.Output,
			CacheRead:  slot.CacheRead,
			CacheWrite: slot.CacheWrite,
			Cost:       slot.Cost,
			Sessions:   len(slot.sessions),
			Commits:    commitsByDay[key],
		}
		st.Days = append(st.Days, day)

		totals.Prompts += day.Prompts
		totals.Turns += day.Turns
		totals.Input += day.Input
		totals.Output += day.Output
		totals.CacheRead += day.CacheRead
		totals.CacheWrite += day.CacheWrite
		totals.Cost += day.Cost
		totals.Commits += day.Commits
		if day.Prompts > 0 || day.Turns > 0 || day.Commits > 0 {
			totals.Days++
			activeDays[key] = true
		}
		if score := [2]int{day.Prompts, day.Turns}; score[0] > busiestScore[0] || (score[0] == busiestScore[0] && score[1] > busiestScore[1]) {
			busiestScore = score
			if score[0] > 0 || score[1] > 0 {
				totals.BusiestDay = key
			}
		}
	}

	totals.Sessions = len(acc.sessions)
	totals.Tokens = totals.Input + totals.Output + totals.CacheRead + totals.CacheWrite
	if base := totals.CacheRead + totals.Input + totals.CacheWrite; base > 0 {
		totals.CacheHit = float64(totals.CacheRead) / float64(base) * 100
	}

	cursor := last
	if !activeDays[cursor.Format(statsDateLayout)] {
		cursor = cursor.AddDate(0, 0, -1)
	}
	for !cursor.Before(first) && activeDays[cursor.Format(statsDateLayout)] {
		totals.Streak++
		cursor = cursor.AddDate(0, 0, -1)
	}
	st.Totals = totals

	seen := map[string]bool{}
	for name, slot := range acc.projects {
		seen[name] = true
		label := name
		if label == "" {
			label = statsOtherProject
		}
		st.Projects = append(st.Projects, StatsProject{
			Name:     label,
			Tokens:   slot.tokens(),
			Cost:     slot.Cost,
			Prompts:  slot.Prompts,
			Sessions: len(slot.sessions),
			Commits:  commitsByProject[name],
			Active:   active[name],
		})
	}
	for _, p := range s.Projects {
		if seen[p.Name] {
			continue
		}
		if commitsByProject[p.Name] == 0 && active[p.Name] == 0 {
			continue
		}
		seen[p.Name] = true
		st.Projects = append(st.Projects, StatsProject{
			Name:    p.Name,
			Commits: commitsByProject[p.Name],
			Active:  active[p.Name],
		})
	}
	sort.Slice(st.Projects, func(i, j int) bool {
		if st.Projects[i].Tokens != st.Projects[j].Tokens {
			return st.Projects[i].Tokens > st.Projects[j].Tokens
		}
		return st.Projects[i].Name < st.Projects[j].Name
	})

	for name, agg := range acc.models {
		st.Models = append(st.Models, StatsModel{
			Model:      name,
			Turns:      agg.Turns,
			Input:      agg.Input,
			Output:     agg.Output,
			CacheRead:  agg.CacheRead,
			CacheWrite: agg.CacheWrite,
			Cost:       agg.Cost,
		})
	}
	sort.Slice(st.Models, func(i, j int) bool {
		if st.Models[i].Cost != st.Models[j].Cost {
			return st.Models[i].Cost > st.Models[j].Cost
		}
		return st.Models[i].Model < st.Models[j].Model
	})

	st.Heatmap = make([][]int, 7)
	for i := range st.Heatmap {
		row := make([]int, 24)
		copy(row, acc.heatmap[i][:])
		st.Heatmap[i] = row
	}
	st.Hours = make([]int, 24)
	copy(st.Hours, acc.hours[:])

	if st.Projects == nil {
		st.Projects = []StatsProject{}
	}
	if st.Models == nil {
		st.Models = []StatsModel{}
	}
	return st
}
