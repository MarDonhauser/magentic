package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// HistoryProvider is a stable, provider-qualified source identity. It is not a
// display label and must not be inferred from a Session name.
type HistoryProvider string

const (
	HistoryProviderClaude  HistoryProvider = "claude"
	HistoryProviderCodex   HistoryProvider = "codex"
	HistoryProviderGemini  HistoryProvider = "gemini"
	HistoryProviderCopilot HistoryProvider = "copilot"
)

var historyProviders = []HistoryProvider{
	HistoryProviderClaude,
	HistoryProviderCodex,
	HistoryProviderGemini,
	HistoryProviderCopilot,
}

func (p HistoryProvider) Label() string {
	switch p {
	case HistoryProviderClaude:
		return "Claude Code"
	case HistoryProviderCodex:
		return "Codex"
	case HistoryProviderGemini:
		return "Gemini CLI"
	case HistoryProviderCopilot:
		return "GitHub Copilot"
	default:
		return string(p)
	}
}

type HistoryFactState string

const (
	HistoryFactKnown         HistoryFactState = "known"
	HistoryFactUnknown       HistoryFactState = "unknown"
	HistoryFactNotApplicable HistoryFactState = "not-applicable"
)

// HistoryFact prevents missing provider facts from being represented as zero
// values. Reason is populated for unknown facts and intentionally kept short.
type HistoryFact[T any] struct {
	State  HistoryFactState `json:"state"`
	Value  T                `json:"value,omitempty"`
	Reason string           `json:"reason,omitempty"`
}

func historyKnown[T any](value T) HistoryFact[T] {
	return HistoryFact[T]{State: HistoryFactKnown, Value: value}
}

func historyUnknown[T any](reason string) HistoryFact[T] {
	return HistoryFact[T]{State: HistoryFactUnknown, Reason: reason}
}

func historyNotApplicable[T any]() HistoryFact[T] {
	return HistoryFact[T]{State: HistoryFactNotApplicable}
}

type HistoryRole string

const (
	HistoryRoleDeveloper HistoryRole = "developer"
	HistoryRoleAssistant HistoryRole = "assistant"
)

type HistoryEventKind string

const (
	HistoryEventPrompt HistoryEventKind = "prompt"
	HistoryEventOutput HistoryEventKind = "output"
	HistoryEventUsage  HistoryEventKind = "usage"
)

type HistoryLineage string

const (
	HistoryLineagePrimary   HistoryLineage = "primary"
	HistoryLineageDelegated HistoryLineage = "delegated"
)

type HistoryUsage struct {
	InputTokens      HistoryFact[int64] `json:"inputTokens"`
	OutputTokens     HistoryFact[int64] `json:"outputTokens"`
	CacheReadTokens  HistoryFact[int64] `json:"cacheReadTokens"`
	CacheWriteTokens HistoryFact[int64] `json:"cacheWriteTokens"`
}

type HistoryAttribution struct {
	ProjectKey  HistoryFact[string] `json:"projectKey"`
	ProjectName HistoryFact[string] `json:"projectName"`
	SessionKey  HistoryFact[string] `json:"sessionKey"`
	SessionName HistoryFact[string] `json:"sessionName"`
}

type HistoryEvent struct {
	ID             string                 `json:"id"`
	SourceID       string                 `json:"sourceId"`
	Provider       HistoryProvider        `json:"provider"`
	ConversationID HistoryFact[string]    `json:"conversationId"`
	OccurredAt     HistoryFact[time.Time] `json:"occurredAt"`
	Role           HistoryRole            `json:"role"`
	Kind           HistoryEventKind       `json:"kind"`
	Lineage        HistoryLineage         `json:"lineage"`
	Text           HistoryFact[string]    `json:"text"`
	Model          HistoryFact[string]    `json:"model"`
	Usage          HistoryUsage           `json:"usage"`
	CWD            HistoryFact[string]    `json:"cwd"`
	Links          []string               `json:"links,omitempty"`
	Attribution    HistoryAttribution     `json:"attribution"`
}

type HistoryProjectAssociation struct {
	Key  string
	Name string
	Path string
}

type HistorySessionAssociation struct {
	Key            string
	Name           string
	ProjectKey     string
	Dir            string
	Provider       HistoryProvider
	ConversationID string
}

// HistoryAssociations is an immutable query input. The WorkHistory index keeps
// source facts only; Project and Session attribution is recomputed for every
// query so renames and later adoption do not require transcript reparsing.
type HistoryAssociations struct {
	Revision string
	Projects []HistoryProjectAssociation
	Sessions []HistorySessionAssociation
}

// HistoryAssociationsFromState is a temporary compatibility translation for
// current callers. Durable Registry IDs are preserved; names are used only
// while reading legacy state that has not yet acquired IDs.
func HistoryAssociationsFromState(state *State) HistoryAssociations {
	var out HistoryAssociations
	if state == nil {
		return out
	}
	for _, project := range state.Projects {
		key := string(project.ID)
		if key == "" {
			key = project.Name
		}
		out.Projects = append(out.Projects, HistoryProjectAssociation{
			Key: key, Name: project.Name, Path: project.Path,
		})
	}
	for _, session := range state.Agents {
		key := string(session.ID)
		if key == "" {
			key = session.Name
		}
		projectKey := string(session.ProjectID)
		if projectKey == "" {
			projectKey = session.Project
		}
		base := HistorySessionAssociation{
			Key:        key,
			Name:       session.Name,
			ProjectKey: projectKey,
			Dir:        session.Dir,
		}
		addedRun := false
		for _, run := range session.AgentRuns {
			provider, ok := historyProviderFromAgentVendor(run.Vendor)
			if !ok || run.ExternalID == "" {
				continue
			}
			association := base
			association.Provider = provider
			association.ConversationID = run.ExternalID
			out.Sessions = append(out.Sessions, association)
			addedRun = true
		}
		// Legacy state stored only Claude's run ID. Keep it readable without
		// allowing that unqualified field to override an AgentRuns binding.
		if !addedRun && session.SessionID != "" && !session.IsTerm() {
			association := base
			association.Provider = HistoryProviderClaude
			association.ConversationID = session.SessionID
			out.Sessions = append(out.Sessions, association)
			addedRun = true
		}
		if !addedRun {
			out.Sessions = append(out.Sessions, base)
		}
	}
	return out
}

func historyProviderFromAgentVendor(vendor AgentVendor) (HistoryProvider, bool) {
	switch vendor {
	case AgentVendorClaude:
		return HistoryProviderClaude, true
	case AgentVendorCodex:
		return HistoryProviderCodex, true
	case AgentVendorGemini:
		return HistoryProviderGemini, true
	case AgentVendorCopilot:
		return HistoryProviderCopilot, true
	default:
		return "", false
	}
}

type HistoryEventQuery struct {
	Since              time.Time
	Before             time.Time
	Providers          []HistoryProvider
	ProjectKeys        []string
	SessionKeys        []string
	Roles              []HistoryRole
	Kinds              []HistoryEventKind
	Lineages           []HistoryLineage
	Text               string
	IncludeUnknownTime bool
	Limit              int
	Offset             int
}

type HistorySourceState string

const (
	HistorySourceAvailable   HistorySourceState = "available"
	HistorySourceAbsent      HistorySourceState = "absent"
	HistorySourcePartial     HistorySourceState = "partial"
	HistorySourceUnavailable HistorySourceState = "unavailable"
)

type HistoryProblem struct {
	Provider HistoryProvider `json:"provider"`
	SourceID string          `json:"sourceId,omitempty"`
	Kind     string          `json:"kind"`
	Message  string          `json:"message"`
}

type HistoryProviderCoverage struct {
	Provider     HistoryProvider    `json:"provider"`
	State        HistorySourceState `json:"state"`
	Files        int                `json:"files"`
	IndexedFiles int                `json:"indexedFiles"`
	ParsedFiles  int                `json:"parsedFiles"`
	ReusedFiles  int                `json:"reusedFiles"`
	Problems     []HistoryProblem   `json:"problems,omitempty"`
}

type HistoryMeta struct {
	Revision            uint64                    `json:"revision"`
	AssociationRevision string                    `json:"associationRevision,omitempty"`
	ObservedAt          time.Time                 `json:"observedAt"`
	Coverage            []HistoryProviderCoverage `json:"coverage"`
}

type HistoryEventPage struct {
	Events []HistoryEvent `json:"events"`
	Total  int            `json:"total"`
	Meta   HistoryMeta    `json:"meta"`
}

type HistoryLinkQuery struct {
	Events   HistoryEventQuery
	Distinct bool
	Limit    int
	Offset   int
}

type HistoryLink struct {
	URL         string                 `json:"url"`
	EventID     string                 `json:"eventId"`
	Provider    HistoryProvider        `json:"provider"`
	OccurredAt  HistoryFact[time.Time] `json:"occurredAt"`
	Attribution HistoryAttribution     `json:"attribution"`
}

type HistoryLinkPage struct {
	Links []HistoryLink `json:"links"`
	Total int           `json:"total"`
	Meta  HistoryMeta   `json:"meta"`
}

type HistoryAggregateCoverage string

const (
	HistoryCoverageComplete HistoryAggregateCoverage = "complete"
	HistoryCoveragePartial  HistoryAggregateCoverage = "partial"
	HistoryCoverageNone     HistoryAggregateCoverage = "none"
)

type HistoryMeasure struct {
	Value         int64                    `json:"value"`
	Coverage      HistoryAggregateCoverage `json:"coverage"`
	KnownEvents   int                      `json:"knownEvents"`
	UnknownEvents int                      `json:"unknownEvents"`
}

type HistoryUsageSummary struct {
	InputTokens      HistoryMeasure `json:"inputTokens"`
	OutputTokens     HistoryMeasure `json:"outputTokens"`
	CacheReadTokens  HistoryMeasure `json:"cacheReadTokens"`
	CacheWriteTokens HistoryMeasure `json:"cacheWriteTokens"`
}

type HistoryTotals struct {
	Prompts      int                 `json:"prompts"`
	Outputs      int                 `json:"outputs"`
	UsageRecords int                 `json:"usageRecords"`
	Sessions     int                 `json:"sessions"`
	Projects     int                 `json:"projects"`
	UnknownTimes int                 `json:"unknownTimes"`
	Usage        HistoryUsageSummary `json:"usage"`
}

type HistoryDaySummary struct {
	Date    string              `json:"date"`
	Prompts int                 `json:"prompts"`
	Outputs int                 `json:"outputs"`
	Usage   HistoryUsageSummary `json:"usage"`
}

type HistoryModelSummary struct {
	Provider HistoryProvider     `json:"provider"`
	Model    string              `json:"model"`
	Turns    int                 `json:"turns"`
	Usage    HistoryUsageSummary `json:"usage"`
}

type HistoryProviderSummary struct {
	Provider HistoryProvider     `json:"provider"`
	State    HistorySourceState  `json:"state"`
	Prompts  int                 `json:"prompts"`
	Outputs  int                 `json:"outputs"`
	Usage    HistoryUsageSummary `json:"usage"`
}

type HistorySummaryQuery struct {
	Events   HistoryEventQuery
	Location *time.Location
}

type HistorySummary struct {
	Totals    HistoryTotals            `json:"totals"`
	Days      []HistoryDaySummary      `json:"days"`
	Models    []HistoryModelSummary    `json:"models"`
	Providers []HistoryProviderSummary `json:"providers"`
	Meta      HistoryMeta              `json:"meta"`
}

type WorkHistoryConfig struct {
	IndexDir  string
	HomeDir   string
	CodexHome string
}

type workHistoryFS interface {
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	WalkDir(string, fs.WalkDirFunc) error
}

type osWorkHistoryFS struct{}

func (osWorkHistoryFS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (osWorkHistoryFS) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (osWorkHistoryFS) WalkDir(path string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(path, fn)
}

// WorkHistory is a local-only Module. It owns one derived index and four
// private provider Adapters; callers cannot select a parser or bypass the
// normalized query semantics.
type WorkHistory struct {
	config   WorkHistoryConfig
	files    workHistoryFS
	adapters []historyProviderAdapter
	now      func() time.Time
	mu       sync.Mutex
}

func OpenWorkHistory(config WorkHistoryConfig) (*WorkHistory, error) {
	if config.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("work history home: %w", err)
		}
		config.HomeDir = home
	}
	if config.CodexHome == "" {
		config.CodexHome = os.Getenv("CODEX_HOME")
		if config.CodexHome == "" {
			config.CodexHome = filepath.Join(config.HomeDir, ".codex")
		}
	}
	if config.IndexDir == "" {
		config.IndexDir = filepath.Join(filepath.Dir(StatePath()), "work-history")
	}
	if !filepath.IsAbs(config.IndexDir) || !filepath.IsAbs(config.HomeDir) || !filepath.IsAbs(config.CodexHome) {
		return nil, errors.New("work history paths must be absolute")
	}
	if err := ensurePrivateHistoryDir(config.IndexDir); err != nil {
		return nil, err
	}
	h := &WorkHistory{
		config: config,
		files:  osWorkHistoryFS{},
		now:    time.Now,
	}
	h.adapters = builtinHistoryAdapters(config)
	return h, nil
}

func ensurePrivateHistoryDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create work history index: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect work history index: %w", err)
	}
	return nil
}

func (h *WorkHistory) Events(ctx context.Context, associations HistoryAssociations, query HistoryEventQuery) (HistoryEventPage, error) {
	index, meta, err := h.refresh(ctx)
	if err != nil {
		return HistoryEventPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, total := queryHistoryEvents(index, associations, query, true)
	if events == nil {
		events = []HistoryEvent{}
	}
	return HistoryEventPage{Events: events, Total: total, Meta: meta}, nil
}

func (h *WorkHistory) Links(ctx context.Context, associations HistoryAssociations, query HistoryLinkQuery) (HistoryLinkPage, error) {
	index, meta, err := h.refresh(ctx)
	if err != nil {
		return HistoryLinkPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	eventQuery := query.Events
	eventQuery.Limit, eventQuery.Offset = 0, 0
	events, _ := queryHistoryEvents(index, associations, eventQuery, false)
	seen := map[string]bool{}
	var links []HistoryLink
	for _, event := range events {
		for _, url := range event.Links {
			if query.Distinct && seen[url] {
				continue
			}
			seen[url] = true
			links = append(links, HistoryLink{
				URL: url, EventID: event.ID, Provider: event.Provider,
				OccurredAt: event.OccurredAt, Attribution: event.Attribution,
			})
		}
	}
	total := len(links)
	limit := query.Limit
	if limit <= 0 {
		limit = 40
	}
	links = historyPage(links, query.Offset, limit)
	if links == nil {
		links = []HistoryLink{}
	}
	return HistoryLinkPage{Links: links, Total: total, Meta: meta}, nil
}

func (h *WorkHistory) Summarize(ctx context.Context, associations HistoryAssociations, query HistorySummaryQuery) (HistorySummary, error) {
	index, meta, err := h.refresh(ctx)
	if err != nil {
		return HistorySummary{}, err
	}
	meta.AssociationRevision = associations.Revision
	eventQuery := query.Events
	eventQuery.Limit, eventQuery.Offset = 0, 0
	events, _ := queryHistoryEvents(index, associations, eventQuery, false)
	return summarizeHistory(events, query.Location, meta, eventQuery.Providers), nil
}

const workHistoryIndexVersion = 1

type historyIndex struct {
	Version  int                         `json:"version"`
	Revision uint64                      `json:"revision"`
	Files    map[string]historyIndexFile `json:"files"`
}

type historyIndexFile struct {
	Provider       HistoryProvider  `json:"provider"`
	AdapterVersion int              `json:"adapterVersion"`
	Digest         string           `json:"digest"`
	Size           int64            `json:"size"`
	ModTime        int64            `json:"modTime"`
	Records        []historyRecord  `json:"records"`
	Problems       []HistoryProblem `json:"problems,omitempty"`
}

type historyUsageRecord struct {
	Input, Output, CacheRead, CacheWrite                     int64
	InputKnown, OutputKnown, CacheReadKnown, CacheWriteKnown bool
}

func (u historyUsageRecord) anyKnown() bool {
	return u.InputKnown || u.OutputKnown || u.CacheReadKnown || u.CacheWriteKnown
}

type historyRecord struct {
	ID             string             `json:"id"`
	SourceID       string             `json:"sourceId"`
	Provider       HistoryProvider    `json:"provider"`
	ConversationID string             `json:"conversationId,omitempty"`
	Timestamp      string             `json:"timestamp,omitempty"`
	Role           HistoryRole        `json:"role"`
	Kind           HistoryEventKind   `json:"kind"`
	Lineage        HistoryLineage     `json:"lineage"`
	Text           string             `json:"text,omitempty"`
	Model          string             `json:"model,omitempty"`
	Usage          historyUsageRecord `json:"usage"`
	CWD            string             `json:"cwd,omitempty"`
	ProjectAlias   string             `json:"projectAlias,omitempty"`
	Links          []string           `json:"links,omitempty"`
	NativeID       string             `json:"nativeId,omitempty"`
}

func (h *WorkHistory) indexPath() string { return filepath.Join(h.config.IndexDir, "index.json") }
func (h *WorkHistory) lockPath() string  { return filepath.Join(h.config.IndexDir, "index.lock") }

func (h *WorkHistory) refresh(ctx context.Context) (*historyIndex, HistoryMeta, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := ensurePrivateHistoryDir(h.config.IndexDir); err != nil {
		return nil, HistoryMeta{}, err
	}
	var index *historyIndex
	var meta HistoryMeta
	err := withWorkHistoryFileLock(ctx, h.lockPath(), func() error {
		var refreshErr error
		index, meta, refreshErr = h.refreshIndex(ctx)
		return refreshErr
	})
	return index, meta, err
}

func (h *WorkHistory) refreshIndex(ctx context.Context) (*historyIndex, HistoryMeta, error) {
	index, existed, err := loadHistoryIndex(h.indexPath())
	if err != nil {
		return nil, HistoryMeta{}, err
	}
	dirty := !existed
	observedAt := h.now().UTC()
	coverage := make([]HistoryProviderCoverage, 0, len(h.adapters))

	for _, adapter := range h.adapters {
		if err := ctx.Err(); err != nil {
			return nil, HistoryMeta{}, err
		}
		inventory := discoverHistoryFiles(ctx, h.files, adapter)
		cov := inventory.coverage
		seen := make(map[string]bool, len(inventory.files))
		for _, path := range inventory.files {
			if err := ctx.Err(); err != nil {
				return nil, HistoryMeta{}, err
			}
			sourceID := historySourceID(adapter.Provider(), path)
			seen[sourceID] = true
			data, err := h.files.ReadFile(path)
			if err != nil {
				cov.Problems = append(cov.Problems, HistoryProblem{Provider: adapter.Provider(), SourceID: sourceID, Kind: "file-unreadable", Message: err.Error()})
				continue
			}
			digest, err := adapter.Fingerprint(h.files, path, data)
			if err != nil {
				cov.Problems = append(cov.Problems, HistoryProblem{Provider: adapter.Provider(), SourceID: sourceID, Kind: "dependency-unreadable", Message: err.Error()})
				continue
			}
			info, statErr := h.files.Stat(path)
			if statErr != nil {
				cov.Problems = append(cov.Problems, HistoryProblem{Provider: adapter.Provider(), SourceID: sourceID, Kind: "file-unavailable", Message: statErr.Error()})
				continue
			}
			old, ok := index.Files[sourceID]
			if ok && old.Provider == adapter.Provider() && old.AdapterVersion == adapter.Version() && old.Digest == digest {
				cov.ReusedFiles++
				if old.Size != info.Size() || old.ModTime != info.ModTime().UnixNano() {
					old.Size, old.ModTime = info.Size(), info.ModTime().UnixNano()
					index.Files[sourceID] = old
					dirty = true
				}
				cov.Problems = append(cov.Problems, old.Problems...)
				continue
			}
			records, problems, parseErr := adapter.Parse(ctx, h.files, path, data)
			for i := range problems {
				problems[i].Provider = adapter.Provider()
				problems[i].SourceID = sourceID
			}
			if parseErr != nil {
				cov.Problems = append(cov.Problems, HistoryProblem{Provider: adapter.Provider(), SourceID: sourceID, Kind: "parse-failed", Message: parseErr.Error()})
				if ok {
					cov.Problems = append(cov.Problems, old.Problems...)
				}
				continue
			}
			records = normalizeHistoryRecords(adapter.Provider(), sourceID, records)
			index.Files[sourceID] = historyIndexFile{
				Provider: adapter.Provider(), AdapterVersion: adapter.Version(),
				Digest: digest, Size: info.Size(), ModTime: info.ModTime().UnixNano(),
				Records: records, Problems: problems,
			}
			cov.ParsedFiles++
			cov.Problems = append(cov.Problems, problems...)
			dirty = true
		}
		if cov.State == HistorySourceAvailable || cov.State == HistorySourceAbsent {
			for sourceID, file := range index.Files {
				if file.Provider == adapter.Provider() && !seen[sourceID] {
					delete(index.Files, sourceID)
					dirty = true
				}
			}
		}
		for _, file := range index.Files {
			if file.Provider == adapter.Provider() {
				cov.IndexedFiles++
			}
		}
		if len(cov.Problems) > 0 {
			if cov.State == HistorySourceAvailable {
				cov.State = HistorySourcePartial
			} else if cov.State == HistorySourceAbsent {
				cov.State = HistorySourceUnavailable
			}
		}
		coverage = append(coverage, cov)
	}
	if dirty {
		index.Revision++
		if err := saveHistoryIndex(h.indexPath(), index); err != nil {
			return nil, HistoryMeta{}, err
		}
	} else if err := protectHistoryIndex(h.indexPath()); err != nil {
		return nil, HistoryMeta{}, err
	}
	return index, HistoryMeta{Revision: index.Revision, ObservedAt: observedAt, Coverage: coverage}, nil
}

func loadHistoryIndex(path string) (*historyIndex, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &historyIndex{Version: workHistoryIndexVersion, Files: map[string]historyIndexFile{}}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read work history index: %w", err)
	}
	var index historyIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, false, fmt.Errorf("decode work history index: %w", err)
	}
	if index.Version != workHistoryIndexVersion {
		return &historyIndex{Version: workHistoryIndexVersion, Files: map[string]historyIndexFile{}}, false, nil
	}
	if index.Files == nil {
		index.Files = map[string]historyIndexFile{}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, false, fmt.Errorf("protect work history index: %w", err)
	}
	return &index, true, nil
}

func saveHistoryIndex(path string, index *historyIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("encode work history index: %w", err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create work history index: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write work history index: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync work history index: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close work history index: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace work history index: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect work history index: %w", err)
	}
	ok = true
	return nil
}

func protectHistoryIndex(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect work history index: %w", err)
	}
	return nil
}

func historySourceID(provider HistoryProvider, path string) string {
	clean, err := filepath.Abs(path)
	if err == nil {
		path = filepath.Clean(clean)
	}
	sum := sha256.Sum256([]byte(string(provider) + "\x00" + path))
	return string(provider) + ":" + hex.EncodeToString(sum[:12])
}

func normalizeHistoryRecords(provider HistoryProvider, sourceID string, records []historyRecord) []historyRecord {
	byID := map[string]historyRecord{}
	order := make([]string, 0, len(records))
	for _, record := range records {
		record.Provider = provider
		record.SourceID = sourceID
		record.Text = cleanHistoryText(record.Text)
		if record.Text != "" {
			record.Links = extractHistoryLinks(record.Text)
		}
		if record.Lineage == "" {
			record.Lineage = HistoryLineagePrimary
		}
		if record.Kind != HistoryEventUsage && record.Text == "" && !record.Usage.anyKnown() {
			continue
		}
		record.ID = stableHistoryRecordID(record)
		if old, exists := byID[record.ID]; exists {
			byID[record.ID] = mergeHistoryRecord(old, record)
			continue
		}
		byID[record.ID] = record
		order = append(order, record.ID)
	}
	out := make([]historyRecord, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func stableHistoryRecordID(record historyRecord) string {
	identity := strings.Join([]string{
		string(record.Provider), record.ConversationID, record.SourceID,
		string(record.Kind), string(record.Role), record.Timestamp,
		strings.TrimSpace(record.Text), record.NativeID,
	}, "\x00")
	// Omit SourceID when a provider conversation is known. This intentionally
	// deduplicates providers that serialize one interaction twice.
	if record.ConversationID != "" && record.NativeID == "" {
		identity = strings.Join([]string{
			string(record.Provider), record.ConversationID,
			string(record.Kind), string(record.Role), record.Timestamp,
			strings.TrimSpace(record.Text),
		}, "\x00")
	}
	sum := sha256.Sum256([]byte(identity))
	return string(record.Provider) + ":event:" + hex.EncodeToString(sum[:16])
}

func mergeHistoryRecord(a, b historyRecord) historyRecord {
	if a.Text == "" {
		a.Text = b.Text
	}
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.CWD == "" {
		a.CWD = b.CWD
	}
	if a.ProjectAlias == "" {
		a.ProjectAlias = b.ProjectAlias
	}
	if !a.Usage.InputKnown && b.Usage.InputKnown {
		a.Usage.Input, a.Usage.InputKnown = b.Usage.Input, true
	}
	if !a.Usage.OutputKnown && b.Usage.OutputKnown {
		a.Usage.Output, a.Usage.OutputKnown = b.Usage.Output, true
	}
	if !a.Usage.CacheReadKnown && b.Usage.CacheReadKnown {
		a.Usage.CacheRead, a.Usage.CacheReadKnown = b.Usage.CacheRead, true
	}
	if !a.Usage.CacheWriteKnown && b.Usage.CacheWriteKnown {
		a.Usage.CacheWrite, a.Usage.CacheWriteKnown = b.Usage.CacheWrite, true
	}
	for _, link := range b.Links {
		if !historyContains(a.Links, link) {
			a.Links = append(a.Links, link)
		}
	}
	return a
}

func queryHistoryEvents(index *historyIndex, associations HistoryAssociations, query HistoryEventQuery, paginate bool) ([]HistoryEvent, int) {
	resolver := newHistoryAssociationResolver(associations)
	byID := map[string]historyRecord{}
	for _, file := range index.Files {
		for _, record := range file.Records {
			if old, ok := byID[record.ID]; ok {
				byID[record.ID] = mergeHistoryRecord(old, record)
			} else {
				byID[record.ID] = record
			}
		}
	}
	providers := historySet(query.Providers)
	projects := historySet(query.ProjectKeys)
	sessions := historySet(query.SessionKeys)
	roles := historySet(query.Roles)
	kinds := historySet(query.Kinds)
	lineages := historySet(query.Lineages)
	needle := strings.ToLower(strings.TrimSpace(query.Text))
	var events []HistoryEvent
	for _, record := range byID {
		if len(providers) > 0 && !providers[record.Provider] || len(roles) > 0 && !roles[record.Role] ||
			len(kinds) > 0 && !kinds[record.Kind] || len(lineages) > 0 && !lineages[record.Lineage] {
			continue
		}
		event := historyEventFromRecord(record, resolver.resolve(record))
		if !query.Since.IsZero() || !query.Before.IsZero() {
			if event.OccurredAt.State != HistoryFactKnown {
				if !query.IncludeUnknownTime {
					continue
				}
			} else if !query.Since.IsZero() && event.OccurredAt.Value.Before(query.Since) ||
				!query.Before.IsZero() && !event.OccurredAt.Value.Before(query.Before) {
				continue
			}
		}
		if len(projects) > 0 && (event.Attribution.ProjectKey.State != HistoryFactKnown || !projects[event.Attribution.ProjectKey.Value]) {
			continue
		}
		if len(sessions) > 0 && (event.Attribution.SessionKey.State != HistoryFactKnown || !sessions[event.Attribution.SessionKey.Value]) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(event.Text.Value), needle) {
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.OccurredAt.State != b.OccurredAt.State {
			return a.OccurredAt.State == HistoryFactKnown
		}
		if a.OccurredAt.State == HistoryFactKnown && !a.OccurredAt.Value.Equal(b.OccurredAt.Value) {
			return a.OccurredAt.Value.After(b.OccurredAt.Value)
		}
		return a.ID < b.ID
	})
	total := len(events)
	if paginate {
		limit := query.Limit
		if limit <= 0 {
			limit = 150
		}
		if limit > 1000 {
			limit = 1000
		}
		events = historyPage(events, query.Offset, limit)
	}
	return events, total
}

func historyEventFromRecord(record historyRecord, attribution HistoryAttribution) HistoryEvent {
	event := HistoryEvent{
		ID: record.ID, SourceID: record.SourceID, Provider: record.Provider,
		Role: record.Role, Kind: record.Kind, Lineage: record.Lineage,
		Links: append([]string(nil), record.Links...), Attribution: attribution,
	}
	if record.ConversationID != "" {
		event.ConversationID = historyKnown(record.ConversationID)
	} else {
		event.ConversationID = historyUnknown[string]("provider did not report a conversation id")
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
		event.OccurredAt = historyKnown(timestamp.UTC())
	} else {
		event.OccurredAt = historyUnknown[time.Time]("provider timestamp missing or invalid")
	}
	if record.Text != "" {
		event.Text = historyKnown(record.Text)
	} else {
		event.Text = historyNotApplicable[string]()
	}
	if record.Kind == HistoryEventPrompt {
		event.Model = historyNotApplicable[string]()
		event.Usage = notApplicableHistoryUsage()
	} else {
		if record.Model != "" {
			event.Model = historyKnown(record.Model)
		} else {
			event.Model = historyUnknown[string]("provider did not report a model")
		}
		event.Usage = publicHistoryUsage(record.Usage)
	}
	if record.CWD != "" {
		event.CWD = historyKnown(record.CWD)
	} else {
		event.CWD = historyUnknown[string]("provider did not report a working directory")
	}
	return event
}

func publicHistoryUsage(usage historyUsageRecord) HistoryUsage {
	return HistoryUsage{
		InputTokens:      historyUsageFact(usage.Input, usage.InputKnown),
		OutputTokens:     historyUsageFact(usage.Output, usage.OutputKnown),
		CacheReadTokens:  historyUsageFact(usage.CacheRead, usage.CacheReadKnown),
		CacheWriteTokens: historyUsageFact(usage.CacheWrite, usage.CacheWriteKnown),
	}
}

func historyUsageFact(value int64, known bool) HistoryFact[int64] {
	if known {
		return historyKnown(value)
	}
	return historyUnknown[int64]("provider did not report this usage fact")
}

func notApplicableHistoryUsage() HistoryUsage {
	return HistoryUsage{
		InputTokens: historyNotApplicable[int64](), OutputTokens: historyNotApplicable[int64](),
		CacheReadTokens: historyNotApplicable[int64](), CacheWriteTokens: historyNotApplicable[int64](),
	}
}

type historyAssociationResolver struct {
	projects      []HistoryProjectAssociation
	sessions      []HistorySessionAssociation
	byProject     map[string]HistoryProjectAssociation
	exactSessions map[string][]HistorySessionAssociation
	aliases       map[string][]HistoryProjectAssociation
}

func newHistoryAssociationResolver(input HistoryAssociations) historyAssociationResolver {
	r := historyAssociationResolver{
		projects:      append([]HistoryProjectAssociation(nil), input.Projects...),
		sessions:      append([]HistorySessionAssociation(nil), input.Sessions...),
		byProject:     map[string]HistoryProjectAssociation{},
		exactSessions: map[string][]HistorySessionAssociation{},
		aliases:       map[string][]HistoryProjectAssociation{},
	}
	for _, project := range r.projects {
		r.byProject[project.Key] = project
		for _, alias := range historyProjectAliases(project.Path) {
			r.aliases[alias] = append(r.aliases[alias], project)
		}
	}
	for _, session := range r.sessions {
		if session.ConversationID != "" {
			key := string(session.Provider) + "\x00" + session.ConversationID
			r.exactSessions[key] = append(r.exactSessions[key], session)
		}
	}
	sort.Slice(r.projects, func(i, j int) bool { return len(r.projects[i].Path) > len(r.projects[j].Path) })
	sort.Slice(r.sessions, func(i, j int) bool { return len(r.sessions[i].Dir) > len(r.sessions[j].Dir) })
	return r
}

func (r historyAssociationResolver) resolve(record historyRecord) HistoryAttribution {
	var session *HistorySessionAssociation
	if record.ConversationID != "" {
		matches := r.exactSessions[string(record.Provider)+"\x00"+record.ConversationID]
		if len(matches) == 1 {
			copy := matches[0]
			session = &copy
		} else if len(matches) > 1 {
			return unknownHistoryAttribution("ambiguous provider conversation binding")
		}
	}
	if session == nil && record.CWD != "" {
		matches := longestHistorySessionMatches(r.sessions, record.CWD)
		if len(matches) == 1 {
			copy := matches[0]
			session = &copy
		} else if len(matches) > 1 {
			return unknownHistoryAttribution("ambiguous session location")
		}
	}
	result := unknownHistoryAttribution("no Registry association")
	if session != nil {
		result.SessionKey = historyKnown(session.Key)
		result.SessionName = historyKnown(session.Name)
		if project, ok := r.byProject[session.ProjectKey]; ok {
			result.ProjectKey = historyKnown(project.Key)
			result.ProjectName = historyKnown(project.Name)
			return result
		}
	}
	var projectMatches []HistoryProjectAssociation
	if record.CWD != "" {
		projectMatches = longestHistoryProjectMatches(r.projects, record.CWD)
	}
	if len(projectMatches) == 0 && record.ProjectAlias != "" {
		projectMatches = r.aliases[record.ProjectAlias]
	}
	if len(projectMatches) == 1 {
		result.ProjectKey = historyKnown(projectMatches[0].Key)
		result.ProjectName = historyKnown(projectMatches[0].Name)
	} else if len(projectMatches) > 1 {
		result.ProjectKey = historyUnknown[string]("ambiguous project association")
		result.ProjectName = historyUnknown[string]("ambiguous project association")
	}
	return result
}

func unknownHistoryAttribution(reason string) HistoryAttribution {
	return HistoryAttribution{
		ProjectKey: historyUnknown[string](reason), ProjectName: historyUnknown[string](reason),
		SessionKey: historyUnknown[string](reason), SessionName: historyUnknown[string](reason),
	}
}

func longestHistoryProjectMatches(projects []HistoryProjectAssociation, path string) []HistoryProjectAssociation {
	clean := filepath.Clean(path)
	best := -1
	var out []HistoryProjectAssociation
	for _, project := range projects {
		if project.Path == "" {
			continue
		}
		root := filepath.Clean(project.Path)
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > best {
			best, out = len(root), []HistoryProjectAssociation{project}
		} else if len(root) == best {
			out = append(out, project)
		}
	}
	return out
}

func longestHistorySessionMatches(sessions []HistorySessionAssociation, path string) []HistorySessionAssociation {
	clean := filepath.Clean(path)
	best := -1
	var out []HistorySessionAssociation
	for _, session := range sessions {
		if session.Dir == "" {
			continue
		}
		root := filepath.Clean(session.Dir)
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > best {
			best, out = len(root), []HistorySessionAssociation{session}
		} else if len(root) == best {
			out = append(out, session)
		}
	}
	return out
}

func historyProjectAliases(path string) []string {
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	hash := sha256.Sum256([]byte(clean))
	encoded := strings.ReplaceAll(clean, string(filepath.Separator), "-")
	return []string{
		filepath.Base(clean),
		hex.EncodeToString(hash[:]),
		encoded,
		strings.TrimPrefix(encoded, "-"),
	}
}

type historyMeasureAcc struct {
	value   int64
	known   int
	unknown int
}

func (a *historyMeasureAcc) add(fact HistoryFact[int64]) {
	switch fact.State {
	case HistoryFactKnown:
		a.value += fact.Value
		a.known++
	case HistoryFactUnknown:
		a.unknown++
	}
}

type historyUsageAcc struct {
	input, output, cacheRead, cacheWrite historyMeasureAcc
}

func (a *historyUsageAcc) add(usage HistoryUsage) {
	a.input.add(usage.InputTokens)
	a.output.add(usage.OutputTokens)
	a.cacheRead.add(usage.CacheReadTokens)
	a.cacheWrite.add(usage.CacheWriteTokens)
}

func (a historyUsageAcc) result(sourceComplete bool) HistoryUsageSummary {
	return HistoryUsageSummary{
		InputTokens:      historyMeasureResult(a.input, sourceComplete),
		OutputTokens:     historyMeasureResult(a.output, sourceComplete),
		CacheReadTokens:  historyMeasureResult(a.cacheRead, sourceComplete),
		CacheWriteTokens: historyMeasureResult(a.cacheWrite, sourceComplete),
	}
}

func historyMeasureResult(acc historyMeasureAcc, sourceComplete bool) HistoryMeasure {
	coverage := HistoryCoverageComplete
	if acc.unknown > 0 || !sourceComplete {
		coverage = HistoryCoveragePartial
	}
	if acc.known == 0 && acc.unknown > 0 {
		coverage = HistoryCoverageNone
	}
	return HistoryMeasure{Value: acc.value, Coverage: coverage, KnownEvents: acc.known, UnknownEvents: acc.unknown}
}

func summarizeHistory(events []HistoryEvent, location *time.Location, meta HistoryMeta, selected []HistoryProvider) HistorySummary {
	if location == nil {
		location = time.Local
	}
	sourceComplete := historySourcesComplete(meta.Coverage, selected)
	dayAcc := map[string]*struct {
		prompts, outputs int
		usage            historyUsageAcc
	}{}
	modelAcc := map[string]*struct {
		provider HistoryProvider
		model    string
		turns    int
		usage    historyUsageAcc
	}{}
	providerAcc := map[HistoryProvider]*struct {
		prompts, outputs int
		usage            historyUsageAcc
	}{}
	var totalUsage historyUsageAcc
	sessions, projects := map[string]bool{}, map[string]bool{}
	result := HistorySummary{Meta: meta}
	for _, provider := range historyProviders {
		providerAcc[provider] = &struct {
			prompts, outputs int
			usage            historyUsageAcc
		}{}
	}
	for _, event := range events {
		pa := providerAcc[event.Provider]
		if pa == nil {
			pa = &struct {
				prompts, outputs int
				usage            historyUsageAcc
			}{}
			providerAcc[event.Provider] = pa
		}
		switch event.Kind {
		case HistoryEventPrompt:
			result.Totals.Prompts++
			pa.prompts++
		case HistoryEventOutput:
			result.Totals.Outputs++
			pa.outputs++
		case HistoryEventUsage:
			result.Totals.UsageRecords++
		}
		if event.Attribution.SessionKey.State == HistoryFactKnown {
			sessions[event.Attribution.SessionKey.Value] = true
		} else if event.ConversationID.State == HistoryFactKnown {
			sessions[string(event.Provider)+"\x00"+event.ConversationID.Value] = true
		}
		if event.Attribution.ProjectKey.State == HistoryFactKnown {
			projects[event.Attribution.ProjectKey.Value] = true
		}
		if event.Kind != HistoryEventPrompt {
			totalUsage.add(event.Usage)
			pa.usage.add(event.Usage)
			model := ""
			if event.Model.State == HistoryFactKnown {
				model = event.Model.Value
			}
			key := string(event.Provider) + "\x00" + model
			ma := modelAcc[key]
			if ma == nil {
				ma = &struct {
					provider HistoryProvider
					model    string
					turns    int
					usage    historyUsageAcc
				}{provider: event.Provider, model: model}
				modelAcc[key] = ma
			}
			if event.Kind == HistoryEventOutput {
				ma.turns++
			}
			ma.usage.add(event.Usage)
		}
		if event.OccurredAt.State != HistoryFactKnown {
			result.Totals.UnknownTimes++
			continue
		}
		date := event.OccurredAt.Value.In(location).Format("2006-01-02")
		da := dayAcc[date]
		if da == nil {
			da = &struct {
				prompts, outputs int
				usage            historyUsageAcc
			}{}
			dayAcc[date] = da
		}
		if event.Kind == HistoryEventPrompt {
			da.prompts++
		} else {
			if event.Kind == HistoryEventOutput {
				da.outputs++
			}
			da.usage.add(event.Usage)
		}
	}
	result.Totals.Sessions = len(sessions)
	result.Totals.Projects = len(projects)
	result.Totals.Usage = totalUsage.result(sourceComplete)
	for date, day := range dayAcc {
		result.Days = append(result.Days, HistoryDaySummary{Date: date, Prompts: day.prompts, Outputs: day.outputs, Usage: day.usage.result(sourceComplete)})
	}
	sort.Slice(result.Days, func(i, j int) bool { return result.Days[i].Date < result.Days[j].Date })
	for _, model := range modelAcc {
		label := model.model
		if label == "" {
			label = "unknown"
		}
		result.Models = append(result.Models, HistoryModelSummary{Provider: model.provider, Model: label, Turns: model.turns, Usage: model.usage.result(sourceComplete)})
	}
	sort.Slice(result.Models, func(i, j int) bool {
		if result.Models[i].Provider != result.Models[j].Provider {
			return result.Models[i].Provider < result.Models[j].Provider
		}
		return result.Models[i].Model < result.Models[j].Model
	})
	coverageByProvider := map[HistoryProvider]HistorySourceState{}
	for _, cov := range meta.Coverage {
		coverageByProvider[cov.Provider] = cov.State
	}
	for _, provider := range historyProviders {
		acc := providerAcc[provider]
		complete := coverageByProvider[provider] == HistorySourceAvailable || coverageByProvider[provider] == HistorySourceAbsent
		result.Providers = append(result.Providers, HistoryProviderSummary{
			Provider: provider, State: coverageByProvider[provider], Prompts: acc.prompts, Outputs: acc.outputs, Usage: acc.usage.result(complete),
		})
	}
	if result.Days == nil {
		result.Days = []HistoryDaySummary{}
	}
	if result.Models == nil {
		result.Models = []HistoryModelSummary{}
	}
	return result
}

func historySourcesComplete(coverage []HistoryProviderCoverage, selected []HistoryProvider) bool {
	wanted := historySet(selected)
	seen := map[HistoryProvider]bool{}
	for _, item := range coverage {
		if len(wanted) > 0 && !wanted[item.Provider] {
			continue
		}
		seen[item.Provider] = true
		if item.State != HistorySourceAvailable && item.State != HistorySourceAbsent {
			return false
		}
	}
	if len(wanted) > 0 {
		for provider := range wanted {
			if !seen[provider] {
				return false
			}
		}
	}
	return true
}

func historySet[T comparable](values []T) map[T]bool {
	out := make(map[T]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func historyPage[T any](values []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(values) {
		return []T{}
	}
	end := offset + limit
	if end > len(values) {
		end = len(values)
	}
	return values[offset:end]
}

func historyContains[T comparable](values []T, wanted T) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
