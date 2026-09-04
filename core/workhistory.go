package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// display label and must not be inferred from a Session name. Its members are
// conversions of the AgentVendor constants, not independent literals, so the
// two enumerations cannot drift apart into two parallel vendor identities.
type HistoryProvider string

const (
	HistoryProviderClaude      HistoryProvider = HistoryProvider(AgentVendorClaude)
	HistoryProviderCodex       HistoryProvider = HistoryProvider(AgentVendorCodex)
	HistoryProviderGemini      HistoryProvider = HistoryProvider(AgentVendorGemini)
	HistoryProviderCopilot     HistoryProvider = HistoryProvider(AgentVendorCopilot)
	HistoryProviderAntigravity HistoryProvider = HistoryProvider(AgentVendorAntigravity)
)

// historyProviders enumerates every known provider by reading the same
// vendor catalog AgentProvider registration uses (builtinAgentProviders),
// rather than a second hand-maintained list that could silently omit a
// vendor added only there.
var historyProviders = func() []HistoryProvider {
	providers := builtinAgentProviders()
	out := make([]HistoryProvider, 0, len(providers))
	for _, provider := range providers {
		out = append(out, HistoryProvider(provider.Vendor()))
	}
	return out
}()

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
	case HistoryProviderAntigravity:
		return "Antigravity"
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
	Key              string
	Name             string
	ProjectKey       string
	Dir              string
	Provider         HistoryProvider
	ConversationID   string
	LocationEvidence HistoryLocationEvidence
}

// HistoryLocationEvidence states why a provider event whose conversation ID
// does not bind exactly may still fall back to a Session directory. A blank
// value is deliberately no evidence: directory overlap alone cannot prove
// that a terminal or agentless Session owns a provider event.
type HistoryLocationEvidence string

const (
	HistoryLocationProviderRun HistoryLocationEvidence = "provider-run"
)

// HistoryAssociations is an immutable query input. The WorkHistory index keeps
// source facts only; Project and Session attribution is recomputed for every
// query so renames and later adoption do not require transcript reparsing.
type HistoryAssociations struct {
	Revision string
	Projects []HistoryProjectAssociation
	Sessions []HistorySessionAssociation
}

// NewHistoryAssociations builds the immutable attribution input for one
// Registry revision. Durable IDs own identity; names remain a read-only legacy
// fallback for Registry data that predates ID migration.
func NewHistoryAssociations(state State) HistoryAssociations {
	var revision string
	if state.Revision > 0 {
		revision = fmt.Sprintf("registry:%d", state.Revision)
	}
	out := HistoryAssociations{Revision: revision}
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
		// Terminal Sessions have no provider-run evidence. Even when legacy or
		// malformed state happens to carry run-shaped fields, their directory is
		// never allowed to claim coding-agent history.
		if session.IsTerm() {
			continue
		}
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
			association.LocationEvidence = HistoryLocationProviderRun
			out.Sessions = append(out.Sessions, association)
			addedRun = true
		}
		// Legacy state stored only Claude's run ID. Keep it readable without
		// allowing that unqualified field to override an AgentRuns binding.
		if !addedRun && session.SessionID != "" {
			association := base
			association.Provider = HistoryProviderClaude
			association.ConversationID = session.SessionID
			association.LocationEvidence = HistoryLocationProviderRun
			out.Sessions = append(out.Sessions, association)
		}
	}
	return out
}

// historyProviderFromAgentVendor turns a Session's AgentVendor into its
// HistoryProvider identity. Both share the same underlying string, so this is
// a membership check against the known vendor catalog (the same one
// AgentProvider registration uses) instead of a hand-maintained switch that a
// new vendor could be added without ever touching.
func historyProviderFromAgentVendor(vendor AgentVendor) (HistoryProvider, bool) {
	if _, known := providerForVendor(vendor); !known {
		return "", false
	}
	return HistoryProvider(vendor), true
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
	Progress            HistoryIndexProgress      `json:"progress"`
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
	// Retention begrenzt, wie weit Roh-Events zurückreichen. Null bedeutet
	// historyRetentionWindow.
	Retention time.Duration
	// SynchronousIndex lässt Abfragen auf einen vollständigen Lauf warten.
	// Nur für Tests und einmalige Werkzeuge gedacht.
	SynchronousIndex bool
}

type workHistoryFS interface {
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	WalkDir(string, fs.WalkDirFunc) error
}

type osWorkHistoryFS struct{}

func (osWorkHistoryFS) Stat(path string) (fs.FileInfo, error)  { return os.Stat(path) }
func (osWorkHistoryFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (osWorkHistoryFS) ReadFile(path string) ([]byte, error)   { return os.ReadFile(path) }
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
	store    *historyStore

	mu        sync.Mutex
	indexing  bool
	lastRunAt time.Time
	progress  HistoryIndexProgress
	counters  map[HistoryProvider]*historyRunCounters
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
	store, err := openHistoryStore(h.dbPath())
	if err != nil {
		return nil, err
	}
	h.store = store
	return h, nil
}

var (
	sharedWorkHistoryOnce sync.Once
	sharedWorkHistory     *WorkHistory
	sharedWorkHistoryErr  error
)

// SharedWorkHistory hält eine Instanz je Prozess. Der Index ist eine
// gemeinsame Datenbank; jede Abfrage eine eigene Instanz zu öffnen würde
// Verbindungen und Hintergrundläufe vervielfachen.
func SharedWorkHistory() (*WorkHistory, error) {
	sharedWorkHistoryOnce.Do(func() {
		sharedWorkHistory, sharedWorkHistoryErr = OpenWorkHistory(WorkHistoryConfig{})
	})
	return sharedWorkHistory, sharedWorkHistoryErr
}

func resetSharedWorkHistoryForTest() {
	sharedWorkHistoryOnce = sync.Once{}
	sharedWorkHistory, sharedWorkHistoryErr = nil, nil
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
	records, meta, err := h.read(ctx, query)
	if err != nil {
		return HistoryEventPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, total := queryHistoryRecords(records, associations, query, true)
	if events == nil {
		events = []HistoryEvent{}
	}
	return HistoryEventPage{Events: events, Total: total, Meta: meta}, nil
}

func (h *WorkHistory) Links(ctx context.Context, associations HistoryAssociations, query HistoryLinkQuery) (HistoryLinkPage, error) {
	eventQuery := query.Events
	eventQuery.Limit, eventQuery.Offset = 0, 0
	records, meta, err := h.read(ctx, eventQuery)
	if err != nil {
		return HistoryLinkPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, _ := queryHistoryRecords(records, associations, eventQuery, false)
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
	eventQuery := query.Events
	eventQuery.Limit, eventQuery.Offset = 0, 0
	records, meta, err := h.read(ctx, eventQuery)
	if err != nil {
		return HistorySummary{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, _ := queryHistoryRecords(records, associations, eventQuery, false)
	return summarizeHistory(events, query.Location, meta, eventQuery.Providers), nil
}

type HistoryConversationQuery struct {
	Since, Before time.Time
	Providers     []HistoryProvider
	ProjectKeys   []string
	SessionKeys   []string
	Limit, Offset int
}

type HistoryConversation struct {
	Provider       HistoryProvider        `json:"provider"`
	ConversationID string                 `json:"conversationId"`
	StartedAt      HistoryFact[time.Time] `json:"startedAt"`
	LastActivityAt HistoryFact[time.Time] `json:"lastActivityAt"`
	Turns          int                    `json:"turns"`
	LastPrompt     HistoryFact[string]    `json:"lastPrompt"`
	Attribution    HistoryAttribution     `json:"attribution"`
}

type HistoryConversationPage struct {
	Conversations []HistoryConversation `json:"conversations"`
	Total         int                   `json:"total"`
	Meta          HistoryMeta           `json:"meta"`
}

// Conversations fasst die Roh-Events des Aufbewahrungsfensters zu Chats
// zusammen. Ältere Chats erscheinen hier nicht mehr; ihre Kennzahlen leben in
// Activity weiter.
func (h *WorkHistory) Conversations(ctx context.Context, associations HistoryAssociations, query HistoryConversationQuery) (HistoryConversationPage, error) {
	eventQuery := HistoryEventQuery{
		Since: query.Since, Before: query.Before, Providers: query.Providers,
		ProjectKeys: query.ProjectKeys, SessionKeys: query.SessionKeys,
		Lineages: []HistoryLineage{HistoryLineagePrimary},
	}
	records, meta, err := h.read(ctx, eventQuery)
	if err != nil {
		return HistoryConversationPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, _ := queryHistoryRecords(records, associations, eventQuery, false)

	type key struct {
		provider       HistoryProvider
		conversationID string
	}
	byKey := map[key]*HistoryConversation{}
	var order []key
	for _, event := range events {
		if event.ConversationID.State != HistoryFactKnown || event.ConversationID.Value == "" {
			continue
		}
		id := key{provider: event.Provider, conversationID: event.ConversationID.Value}
		conversation := byKey[id]
		if conversation == nil {
			conversation = &HistoryConversation{
				Provider: event.Provider, ConversationID: event.ConversationID.Value,
				StartedAt:      historyUnknown[time.Time]("kein bekannter Zeitpunkt"),
				LastActivityAt: historyUnknown[time.Time]("kein bekannter Zeitpunkt"),
				LastPrompt:     historyUnknown[string]("kein Prompt im Fenster"),
				Attribution:    event.Attribution,
			}
			byKey[id] = conversation
			order = append(order, id)
		}
		if event.Kind == HistoryEventOutput {
			conversation.Turns++
		}
		if event.OccurredAt.State != HistoryFactKnown {
			continue
		}
		when := event.OccurredAt.Value
		if conversation.StartedAt.State != HistoryFactKnown || when.Before(conversation.StartedAt.Value) {
			conversation.StartedAt = historyKnown(when)
		}
		if conversation.LastActivityAt.State != HistoryFactKnown || when.After(conversation.LastActivityAt.Value) {
			conversation.LastActivityAt = historyKnown(when)
		}
		if event.Kind == HistoryEventPrompt && event.Text.State == HistoryFactKnown {
			// queryHistoryRecords liefert absteigend; der erste Prompt ist der jüngste.
			if conversation.LastPrompt.State != HistoryFactKnown {
				conversation.LastPrompt = historyKnown(event.Text.Value)
			}
		}
	}
	out := make([]HistoryConversation, 0, len(order))
	for _, id := range order {
		out = append(out, *byKey[id])
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.LastActivityAt.State != b.LastActivityAt.State {
			return a.LastActivityAt.State == HistoryFactKnown
		}
		if a.LastActivityAt.State == HistoryFactKnown && !a.LastActivityAt.Value.Equal(b.LastActivityAt.Value) {
			return a.LastActivityAt.Value.After(b.LastActivityAt.Value)
		}
		return a.ConversationID < b.ConversationID
	})
	total := len(out)
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	out = historyPage(out, query.Offset, limit)
	if out == nil {
		out = []HistoryConversation{}
	}
	return HistoryConversationPage{Conversations: out, Total: total, Meta: meta}, nil
}

type HistoryActivityQuery struct {
	Since, Before time.Time
	Providers     []HistoryProvider
	Location      *time.Location
}

type HistoryActivityBucket struct {
	Day                     string             `json:"day"`
	Hour                    int                `json:"hour"`
	Provider                HistoryProvider    `json:"provider"`
	ConversationID          string             `json:"conversationId"`
	Model                   string             `json:"model"`
	Prompts                 int                `json:"prompts"`
	Turns                   int                `json:"turns"`
	Usage                   HistoryUsage       `json:"usage"`
	Cost                    float64            `json:"cost"`
	PricedEvents            int                `json:"pricedEvents"`
	UnpricedEvents          int                `json:"unpricedEvents"`
	KnownInputEvents        int                `json:"knownInputEvents"`
	UnknownInputEvents      int                `json:"unknownInputEvents"`
	KnownOutputEvents       int                `json:"knownOutputEvents"`
	UnknownOutputEvents     int                `json:"unknownOutputEvents"`
	KnownCacheReadEvents    int                `json:"knownCacheReadEvents"`
	UnknownCacheReadEvents  int                `json:"unknownCacheReadEvents"`
	KnownCacheWriteEvents   int                `json:"knownCacheWriteEvents"`
	UnknownCacheWriteEvents int                `json:"unknownCacheWriteEvents"`
	Attribution             HistoryAttribution `json:"attribution"`
}

type HistoryActivityPage struct {
	Buckets []HistoryActivityBucket `json:"buckets"`
	Meta    HistoryMeta             `json:"meta"`
}

// Activity liefert die dauerhaften Kennzahlen. Sie überdauern das
// Aufbewahrungsfenster der Roh-Events und tragen die Merkmale, aus denen
// dieselbe Attribution entsteht wie bei Events.
func (h *WorkHistory) Activity(ctx context.Context, associations HistoryAssociations, query HistoryActivityQuery) (HistoryActivityPage, error) {
	h.ensureIndexing(ctx)
	rows, err := h.store.activityRows(ctx, query.Since, query.Before, query.Providers, query.Location)
	if err != nil {
		return HistoryActivityPage{}, err
	}
	meta, err := h.snapshotMeta(ctx)
	if err != nil {
		return HistoryActivityPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	resolver := newHistoryAssociationResolver(associations)
	buckets := make([]HistoryActivityBucket, 0, len(rows))
	for _, row := range rows {
		attribution := resolver.resolve(historyRecord{
			Provider: row.Provider, ConversationID: row.ConversationID,
			CWD: row.CWD, ProjectAlias: row.ProjectAlias,
		})
		buckets = append(buckets, HistoryActivityBucket{
			Day: row.Day, Hour: row.Hour, Provider: row.Provider,
			ConversationID: row.ConversationID, Model: row.Model,
			Prompts: row.Prompts, Turns: row.Turns,
			Usage: HistoryUsage{
				InputTokens:      historyKnown(row.Input),
				OutputTokens:     historyKnown(row.Output),
				CacheReadTokens:  historyKnown(row.CacheRead),
				CacheWriteTokens: historyKnown(row.CacheWrite),
			},
			Cost: row.Cost, PricedEvents: row.PricedEvents, UnpricedEvents: row.UnpricedEvents,
			KnownInputEvents: row.KnownInputEvents, UnknownInputEvents: row.UnknownInputEvents,
			KnownOutputEvents: row.KnownOutputEvents, UnknownOutputEvents: row.UnknownOutputEvents,
			KnownCacheReadEvents: row.KnownCacheReadEvents, UnknownCacheReadEvents: row.UnknownCacheReadEvents,
			KnownCacheWriteEvents: row.KnownCacheWriteEvents, UnknownCacheWriteEvents: row.UnknownCacheWriteEvents,
			Attribution: attribution,
		})
	}
	return HistoryActivityPage{Buckets: buckets, Meta: meta}, nil
}

// read stößt bei Bedarf einen Indexlauf an und liest anschließend die passenden
// Datensätze samt Zustandsbericht aus dem Speicher.
func (h *WorkHistory) read(ctx context.Context, query HistoryEventQuery) ([]historyRecord, HistoryMeta, error) {
	h.ensureIndexing(ctx)
	records, err := h.store.records(ctx, historyFilterFor(query))
	if err != nil {
		return nil, HistoryMeta{}, err
	}
	meta, err := h.snapshotMeta(ctx)
	if err != nil {
		return nil, HistoryMeta{}, err
	}
	return records, meta, nil
}

func historyFilterFor(query HistoryEventQuery) historyRecordFilter {
	return historyRecordFilter{
		Since: query.Since, Before: query.Before, IncludeUnknownTime: query.IncludeUnknownTime,
		Providers: query.Providers, Roles: query.Roles, Kinds: query.Kinds,
		Lineages: query.Lineages, Text: query.Text,
	}
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

func (h *WorkHistory) dbPath() string   { return filepath.Join(h.config.IndexDir, "history.db") }
func (h *WorkHistory) lockPath() string { return filepath.Join(h.config.IndexDir, "index.lock") }
func (h *WorkHistory) Close() error     { return h.store.Close() }

// retention liefert das wirksame Aufbewahrungsfenster der Roh-Events.
func (h *WorkHistory) retention() time.Duration {
	if h.config.Retention > 0 {
		return h.config.Retention
	}
	return historyRetentionWindow
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

// queryHistoryRecords wendet die Filter an, die erst nach dem Auflösen der
// Registry-Zuordnung entschieden werden können, und sortiert das Ergebnis. Die
// Filter nach Provider, Rolle, Art, Abstammung, Zeitfenster und Text hat der
// Speicher bereits erledigt.
func queryHistoryRecords(records []historyRecord, associations HistoryAssociations, query HistoryEventQuery, paginate bool) ([]HistoryEvent, int) {
	resolver := newHistoryAssociationResolver(associations)
	projects := historySet(query.ProjectKeys)
	sessions := historySet(query.SessionKeys)
	var events []HistoryEvent
	for _, record := range records {
		event := historyEventFromRecord(record, resolver.resolve(record))
		if len(projects) > 0 && (event.Attribution.ProjectKey.State != HistoryFactKnown || !projects[event.Attribution.ProjectKey.Value]) {
			continue
		}
		if len(sessions) > 0 && (event.Attribution.SessionKey.State != HistoryFactKnown || !sessions[event.Attribution.SessionKey.Value]) {
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
		matches := uniqueHistorySessionMatches(r.exactSessions[string(record.Provider)+"\x00"+record.ConversationID])
		if len(matches) == 1 {
			copy := matches[0]
			session = &copy
		} else if len(matches) > 1 {
			return unknownHistoryAttribution("ambiguous provider conversation binding")
		}
	}
	if session == nil && record.CWD != "" {
		matches := longestHistorySessionMatches(r.sessions, record.CWD, record.Provider)
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

func longestHistorySessionMatches(sessions []HistorySessionAssociation, path string, provider HistoryProvider) []HistorySessionAssociation {
	clean := filepath.Clean(path)
	best := -1
	var out []HistorySessionAssociation
	for _, session := range sessions {
		if session.LocationEvidence != HistoryLocationProviderRun || session.Provider != provider {
			continue
		}
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
	// A Registry Session may have several provider-qualified AgentRuns and thus
	// several associations for the same directory. Explicit provider-run
	// evidence is applied before path ranking so a terminal, agentless, or
	// incompatible nested Session cannot hide a compatible parent. Collapse
	// remaining aliases by durable Session key.
	return uniqueHistorySessionMatches(out)
}

func uniqueHistorySessionMatches(sessions []HistorySessionAssociation) []HistorySessionAssociation {
	seen := make(map[string]bool, len(sessions))
	out := make([]HistorySessionAssociation, 0, len(sessions))
	for _, session := range sessions {
		key := session.Key
		if key == "" {
			// Hand-built query inputs need not carry Registry IDs. Do not collapse
			// distinct anonymous associations merely because their key is empty.
			key = strings.Join([]string{
				session.Name, session.ProjectKey, session.Dir, string(session.Provider),
				session.ConversationID, string(session.LocationEvidence),
			}, "\x00")
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, session)
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
