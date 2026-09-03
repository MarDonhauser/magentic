package core

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed agents/*.yaml
var shippedAgentManifests embed.FS

// AgentKindSource names where a manifest came from. A user manifest replaces a
// shipped one of the same identifier in full; the two are never merged.
type AgentKindSource string

const (
	AgentKindShipped AgentKindSource = "shipped"
	AgentKindUser    AgentKindSource = "user"
)

// agentKindPattern is one marker: either a case-insensitive literal substring
// or a regular expression. Exactly one of the two is set.
type agentKindPattern struct {
	literal string
	regex   *regexp.Regexp
}

// agentKindBlockedDetail turns a recognized approval dialog into the short
// German label the Observation carries as its detail.
type agentKindBlockedDetail struct {
	label    string
	patterns []agentKindPattern
}

// agentKindWorkingDetail counts what the agent reports it is waiting on. It
// counts either occurrences of a marker line or the last capture group of a
// pattern that spells the number out.
type agentKindWorkingDetail struct {
	occurrences *regexp.Regexp
	capture     *regexp.Regexp
	singular    string
	plural      string
}

// agentKind is one loaded and validated detection manifest.
type agentKind struct {
	id              string
	label           string
	tool            string
	observedVersion string
	screensRecorded bool
	tail            int
	paneCommands    []agentKindPattern
	working         []agentKindPattern
	blocked         []agentKindPattern
	done            []agentKindPattern
	idle            []agentKindPattern
	composer        []agentKindPattern
	blockedDetails  []agentKindBlockedDetail
	workingDetails  []agentKindWorkingDetail
	source          AgentKindSource
	path            string
}

// AgentKindReport is one line of the manifest validation surface: which kind a
// file claims, where it came from, and either that it loaded or why it did not.
type AgentKindReport struct {
	Kind      string          `json:"kind"`
	Label     string          `json:"label,omitempty"`
	Source    AgentKindSource `json:"source"`
	Path      string          `json:"path"`
	Accepted  bool            `json:"accepted"`
	Reason    string          `json:"reason,omitempty"`
	Overruled bool            `json:"overruled,omitempty"`
}

// --- YAML shape -------------------------------------------------------------

type agentKindFile struct {
	Kind            string                            `yaml:"kind"`
	Label           string                            `yaml:"label"`
	Tool            string                            `yaml:"tool"`
	ObservedVersion string                            `yaml:"observed_version"`
	ScreensRecorded *bool                             `yaml:"screens_recorded"`
	Tail            int                               `yaml:"tail"`
	PaneCommands    []agentKindPatternFile            `yaml:"pane_commands"`
	States          map[string][]agentKindPatternFile `yaml:"states"`
	Composer        []agentKindPatternFile            `yaml:"composer"`
	Details         agentKindDetailsFile              `yaml:"details"`
}

type agentKindPatternFile struct {
	Literal string `yaml:"literal"`
	Regex   string `yaml:"regex"`
}

type agentKindDetailsFile struct {
	Blocked []agentKindBlockedDetailFile `yaml:"blocked"`
	Working []agentKindWorkingDetailFile `yaml:"working"`
}

type agentKindBlockedDetailFile struct {
	Label    string                 `yaml:"label"`
	Patterns []agentKindPatternFile `yaml:"patterns"`
}

type agentKindWorkingDetailFile struct {
	Occurrences string `yaml:"occurrences"`
	Capture     string `yaml:"capture"`
	Singular    string `yaml:"singular"`
	Plural      string `yaml:"plural"`
}

// agentKindStates fixes the evaluation order in the format, not in the file:
// a manifest cannot make its own kind read blocked before working.
var agentKindStates = []string{"working", "blocked", "done", "idle"}

// --- parsing and validation -------------------------------------------------

func parseAgentKind(path string, data []byte, source AgentKindSource) (*agentKind, error) {
	var file agentKindFile
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("Manifest ist kein gültiges YAML: %v", err)
	}
	kind := &agentKind{
		id:              strings.TrimSpace(file.Kind),
		label:           strings.TrimSpace(file.Label),
		tool:            strings.TrimSpace(file.Tool),
		observedVersion: strings.TrimSpace(file.ObservedVersion),
		screensRecorded: file.ScreensRecorded == nil || *file.ScreensRecorded,
		tail:            file.Tail,
		source:          source,
		path:            path,
	}
	if kind.id == "" {
		return nil, fmt.Errorf("Manifest nennt keine Agent-Art")
	}
	if kind.label == "" {
		return nil, fmt.Errorf("Agent-Art %q nennt keine Bezeichnung", kind.id)
	}
	if kind.tool == "" {
		kind.tool = kind.id
	}
	switch {
	case kind.tail == 0:
		kind.tail = defaultAgentKindTail
	case kind.tail < 0:
		return nil, fmt.Errorf("Agent-Art %q: tail muss positiv sein", kind.id)
	case kind.tail > observationScrollbackLines:
		return nil, fmt.Errorf(
			"Agent-Art %q: tail %d überschreitet den beobachteten Rückblick von %d Zeilen",
			kind.id, kind.tail, observationScrollbackLines,
		)
	}

	var err error
	if kind.paneCommands, err = compileAgentKindPatterns(kind.id, "pane_commands", file.PaneCommands); err != nil {
		return nil, err
	}
	if len(kind.paneCommands) == 0 {
		return nil, fmt.Errorf("Agent-Art %q nennt kein Pane-Kommando", kind.id)
	}
	for state, patterns := range file.States {
		compiled, compileErr := compileAgentKindPatterns(kind.id, "states."+state, patterns)
		if compileErr != nil {
			return nil, compileErr
		}
		switch state {
		case "working":
			kind.working = compiled
		case "blocked":
			kind.blocked = compiled
		case "done":
			kind.done = compiled
		case "idle":
			kind.idle = compiled
		default:
			return nil, fmt.Errorf(
				"Agent-Art %q: Zustand %q gehört nicht zum Vokabular %s",
				kind.id, state, strings.Join(agentKindStates, ", "),
			)
		}
	}
	if kind.composer, err = compileAgentKindPatterns(kind.id, "composer", file.Composer); err != nil {
		return nil, err
	}
	for _, detail := range file.Details.Blocked {
		label := strings.TrimSpace(detail.Label)
		if label == "" {
			return nil, fmt.Errorf("Agent-Art %q: eine blocked-Detailregel nennt keine Bezeichnung", kind.id)
		}
		patterns, detailErr := compileAgentKindPatterns(kind.id, "details.blocked."+label, detail.Patterns)
		if detailErr != nil {
			return nil, detailErr
		}
		if len(patterns) == 0 {
			return nil, fmt.Errorf("Agent-Art %q: Detailregel %q nennt kein Muster", kind.id, label)
		}
		kind.blockedDetails = append(kind.blockedDetails, agentKindBlockedDetail{label: label, patterns: patterns})
	}
	for index, detail := range file.Details.Working {
		compiled := agentKindWorkingDetail{
			singular: strings.TrimSpace(detail.Singular),
			plural:   strings.TrimSpace(detail.Plural),
		}
		if compiled.singular == "" || compiled.plural == "" {
			return nil, fmt.Errorf("Agent-Art %q: working-Detailregel %d braucht singular und plural", kind.id, index+1)
		}
		if detail.Occurrences == "" && detail.Capture == "" {
			return nil, fmt.Errorf("Agent-Art %q: working-Detailregel %d zählt nichts", kind.id, index+1)
		}
		if detail.Occurrences != "" {
			if compiled.occurrences, err = regexp.Compile(detail.Occurrences); err != nil {
				return nil, fmt.Errorf(
					"Agent-Art %q: working-Detailregel %d: Ausdruck %q lässt sich nicht übersetzen: %v",
					kind.id, index+1, detail.Occurrences, err,
				)
			}
		}
		if detail.Capture != "" {
			if compiled.capture, err = regexp.Compile(detail.Capture); err != nil {
				return nil, fmt.Errorf(
					"Agent-Art %q: working-Detailregel %d: Ausdruck %q lässt sich nicht übersetzen: %v",
					kind.id, index+1, detail.Capture, err,
				)
			}
			if compiled.capture.NumSubexp() < 1 {
				return nil, fmt.Errorf(
					"Agent-Art %q: working-Detailregel %d: capture braucht eine Klammer für die Zahl",
					kind.id, index+1,
				)
			}
		}
		kind.workingDetails = append(kind.workingDetails, compiled)
	}
	if kind.screensRecorded && len(kind.working)+len(kind.blocked)+len(kind.done)+len(kind.idle) == 0 {
		return nil, fmt.Errorf(
			"Agent-Art %q nennt keine Status-Regel; ohne aufgenommene Bildschirme gehört screens_recorded: false ins Manifest",
			kind.id,
		)
	}
	return kind, nil
}

func compileAgentKindPatterns(kindID, field string, patterns []agentKindPatternFile) ([]agentKindPattern, error) {
	compiled := make([]agentKindPattern, 0, len(patterns))
	for index, pattern := range patterns {
		literal := pattern.Literal
		switch {
		case literal != "" && pattern.Regex != "":
			return nil, fmt.Errorf("Agent-Art %q: %s[%d] nennt literal und regex zugleich", kindID, field, index+1)
		case literal != "":
			compiled = append(compiled, agentKindPattern{literal: strings.ToLower(literal)})
		case pattern.Regex != "":
			expression, err := regexp.Compile(pattern.Regex)
			if err != nil {
				return nil, fmt.Errorf(
					"Agent-Art %q: %s[%d]: Ausdruck %q lässt sich nicht übersetzen: %v",
					kindID, field, index+1, pattern.Regex, err,
				)
			}
			compiled = append(compiled, agentKindPattern{regex: expression})
		default:
			return nil, fmt.Errorf("Agent-Art %q: %s[%d] nennt weder literal noch regex", kindID, field, index+1)
		}
	}
	return compiled, nil
}

// --- loading ----------------------------------------------------------------

type agentKindSet struct {
	byID    map[string]*agentKind
	ordered []*agentKind
	reports []AgentKindReport
}

var (
	agentKindMu    sync.Mutex
	agentKindCache = map[string]*agentKindSet{}
)

// AgentKindUserDir is the directory in which a developer may add a manifest for
// an agent Magentic does not ship, or override a shipped one. It sits next to
// Magentic's own state so both travel together.
func AgentKindUserDir() string {
	return filepath.Join(filepath.Dir(StatePath()), "agents")
}

// ReloadAgentKinds drops the loaded manifest set so the next Observation reads
// the files again. Running Sessions and Observation history are untouched.
func ReloadAgentKinds() {
	agentKindMu.Lock()
	defer agentKindMu.Unlock()
	agentKindCache = map[string]*agentKindSet{}
}

func loadedAgentKinds() *agentKindSet {
	dir := AgentKindUserDir()
	agentKindMu.Lock()
	defer agentKindMu.Unlock()
	if set, ok := agentKindCache[dir]; ok {
		return set
	}
	set := buildAgentKindSet(dir)
	agentKindCache[dir] = set
	return set
}

func buildAgentKindSet(userDir string) *agentKindSet {
	set := &agentKindSet{byID: map[string]*agentKind{}}
	shipped := readShippedAgentKinds()
	user := readUserAgentKinds(userDir)

	// A shipped manifest is the floor: it stays in effect unless a *valid* user
	// manifest replaces its kind in full.
	accepted := map[string]*agentKind{}
	for _, loaded := range shipped {
		if loaded.err != nil {
			set.reports = append(set.reports, AgentKindReport{
				Kind: loaded.id, Source: AgentKindShipped, Path: loaded.path,
				Accepted: false, Reason: loaded.err.Error(),
			})
			continue
		}
		if _, taken := accepted[loaded.kind.id]; taken {
			set.reports = append(set.reports, AgentKindReport{
				Kind: loaded.kind.id, Label: loaded.kind.label, Source: AgentKindShipped, Path: loaded.path,
				Accepted: false,
				Reason:   fmt.Sprintf("Agent-Art %q ist bereits vergeben", loaded.kind.id),
			})
			continue
		}
		accepted[loaded.kind.id] = loaded.kind
		set.reports = append(set.reports, AgentKindReport{
			Kind: loaded.kind.id, Label: loaded.kind.label, Source: AgentKindShipped,
			Path: loaded.path, Accepted: true,
		})
	}
	overridden := map[string]bool{}
	for _, loaded := range user {
		if loaded.err != nil {
			set.reports = append(set.reports, AgentKindReport{
				Kind: loaded.id, Source: AgentKindUser, Path: loaded.path,
				Accepted: false, Reason: loaded.err.Error(),
			})
			continue
		}
		if overridden[loaded.kind.id] {
			set.reports = append(set.reports, AgentKindReport{
				Kind: loaded.kind.id, Label: loaded.kind.label, Source: AgentKindUser, Path: loaded.path,
				Accepted: false,
				Reason:   fmt.Sprintf("Agent-Art %q ist bereits vergeben", loaded.kind.id),
			})
			continue
		}
		overridden[loaded.kind.id] = true
		accepted[loaded.kind.id] = loaded.kind
		set.reports = append(set.reports, AgentKindReport{
			Kind: loaded.kind.id, Label: loaded.kind.label, Source: AgentKindUser,
			Path: loaded.path, Accepted: true,
		})
	}
	for index, report := range set.reports {
		if report.Source == AgentKindShipped && report.Accepted && overridden[report.Kind] {
			set.reports[index].Overruled = true
		}
	}
	set.byID = accepted
	for _, kind := range accepted {
		set.ordered = append(set.ordered, kind)
	}
	sort.Slice(set.ordered, func(i, j int) bool { return set.ordered[i].id < set.ordered[j].id })
	sort.SliceStable(set.reports, func(i, j int) bool {
		if set.reports[i].Kind != set.reports[j].Kind {
			return set.reports[i].Kind < set.reports[j].Kind
		}
		return set.reports[i].Source < set.reports[j].Source
	})
	return set
}

type loadedAgentKind struct {
	id   string
	path string
	kind *agentKind
	err  error
}

func readShippedAgentKinds() []loadedAgentKind {
	entries, err := fs.ReadDir(shippedAgentManifests, "agents")
	if err != nil {
		return nil
	}
	loaded := make([]loadedAgentKind, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := "agents/" + entry.Name()
		data, readErr := shippedAgentManifests.ReadFile(path)
		if readErr != nil {
			loaded = append(loaded, loadedAgentKind{path: path, err: readErr})
			continue
		}
		loaded = append(loaded, parsedAgentKind(path, data, AgentKindShipped))
	}
	return loaded
}

func readUserAgentKinds(dir string) []loadedAgentKind {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	loaded := make([]loadedAgentKind, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			loaded = append(loaded, loadedAgentKind{path: path, err: readErr})
			continue
		}
		loaded = append(loaded, parsedAgentKind(path, data, AgentKindUser))
	}
	return loaded
}

func parsedAgentKind(path string, data []byte, source AgentKindSource) loadedAgentKind {
	kind, err := parseAgentKind(path, data, source)
	if err != nil {
		return loadedAgentKind{id: agentKindIDGuess(data), path: path, err: err}
	}
	return loadedAgentKind{id: kind.id, path: path, kind: kind}
}

// agentKindIDGuess recovers the claimed identifier of a rejected manifest so the
// validation surface can still name the kind the developer meant.
func agentKindIDGuess(data []byte) string {
	var probe struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Kind)
}

// --- lookups ----------------------------------------------------------------

// ValidateAgentKinds reports every shipped and user manifest with its kind, its
// source, and either an accepted result or the reason it was rejected.
func ValidateAgentKinds() []AgentKindReport {
	ReloadAgentKinds()
	return append([]AgentKindReport(nil), loadedAgentKinds().reports...)
}

func agentKindForPaneCommand(paneCommand string) (*agentKind, bool) {
	command := normalizedAgentPaneCommand(paneCommand)
	if command == "" {
		return nil, false
	}
	for _, kind := range loadedAgentKinds().ordered {
		if kind.matchesPaneCommand(command) {
			return kind, true
		}
	}
	return nil, false
}

func agentKindForID(id string) (*agentKind, bool) {
	kind, ok := loadedAgentKinds().byID[id]
	return kind, ok
}

// agentKindForTool resolves a canonical tool identity — what DetectAgentTool
// reports — to its manifest. Tool and pane command coincide for most vendors,
// but not for all (agy runs Antigravity), so raw pane-command matching stays
// the fallback rather than the only path.
func agentKindForTool(tool string) (*agentKind, bool) {
	needle := strings.ToLower(strings.TrimSpace(tool))
	if needle != "" {
		for _, kind := range loadedAgentKinds().ordered {
			if strings.ToLower(strings.TrimSpace(kind.tool)) == needle {
				return kind, true
			}
		}
	}
	return agentKindForPaneCommand(tool)
}

func normalizedAgentPaneCommand(command string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(command)), "-")
}

func (k *agentKind) matchesPaneCommand(command string) bool {
	for _, pattern := range k.paneCommands {
		switch {
		case pattern.regex != nil:
			if pattern.regex.MatchString(command) {
				return true
			}
		case pattern.literal != "":
			if command == pattern.literal {
				return true
			}
		}
	}
	return false
}
