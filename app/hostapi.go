package main

import (
	"magentic/core"
)

// HostAPI ist die Nahtstelle aus D1 des Changes add-remote-client-access: die
// existierende app-Binding-Fläche als Go-Interface. Die lokale
// Implementierung ist *App selbst (heutiger Code, nur verschoben gedacht);
// die remote-Implementierung tippt über das Netz auf denselben
// Methodennamen. Das Frontend ruft weiterhin dieselben Bindings.
//
// Die Methodennamen müssen exakt remote.HostAPIMethods entsprechen und jede
// Methode muss in remote.RemoteActionPolicy klassifiziert sein —
// hostapi_test.go prüft beides, damit eine neue Binding-Methode ohne
// Policy-Entscheidung nicht durchrutscht.
type HostAPI interface {
	AddDivider(name string) (string, error)
	AddProject(path string) (string, error)
	AddReviewComment(sessionID, path string, oldStart, oldEnd, newStart, newEnd int, quoted, text, mode string) (core.ReviewComment, error)
	AgentVendors() []core.AgentVendorOption
	ArgoLogin()
	AzAccounts() []AzAccount
	AzLogin()
	AzSetSubscription(id string) error
	Board(projectID string) (core.Board, error)
	BoardArchive(projectID string, limit int) (core.Board, error)
	BreakConfig() core.BreakConfig
	BreakHeartbeat(active bool) core.BreakAdvice
	BreakOver()
	Breaks() (core.BreakAdvice, error)
	BuildInfo() string
	Cleanup(projectID, reference string) (string, error)
	ClearNotch(id string) error
	CloseTerm(connectionKey string)
	CompleteCommands(sessionID, query string) ([]core.SlashCommand, error)
	CompleteFiles(sessionID, query string) ([]string, error)
	DeleteReviewComment(sessionID, commentID string) error
	DeleteSessionAutomation(sessionID, automationID string) error
	Deploy(projectID string) (string, error)
	DeployStatus() DeployStatus
	DiscardQueuedMessage(sessionID, messageID string) error
	DiscardSentReview(sessionID, reviewID string) error
	DiscardSession(sessionID string) error
	DoneAgent(sessionID string) error
	EditReviewComment(sessionID, commentID, text string) error
	EndBreak() error
	FreshStartSession(sessionID string) error
	GitGraph(projectID string, limit int) (core.GitGraph, error)
	HandoffSession(sourceID, targetID string) error
	Inbox() core.OvInbox
	KillSession(sessionID, legacyDockName string) error
	LaterSession(sessionID string) error
	MarkSeen(sessionID string) error
	Merge(projectID, source, target string) (string, error)
	MigrateDockSessions(names []string) ([]DockSessionRef, error)
	MoveSidebarItem(kind, ref, parentKind, parent string, order []core.SidebarRef) error
	NewDockSession(projectID string) (DockSessionRef, error)
	NewSession(projectID string, worktree bool, name string) (string, error)
	NewSessionWithVendor(projectID string, worktree bool, name, vendor string) (string, error)
	NewTermSession(projectID string, worktree bool, name string) (string, error)
	NewTermSessionFor(sessionID string) (string, error)
	NotificationsEnabled() bool
	OpenReview(sessionID string) (*core.SessionReview, error)
	OpenTerm(sessionID, legacyDockName string, cols, rows int) error
	Overview(fresh bool) (core.Overview, error)
	PickFolder() (string, error)
	PromptLinePattern(sessionID string) string
	RemoveDivider(dividerID string) error
	RemoveProject(projectID string) error
	RemoveWorktree(projectID, reference string) error
	RenameDivider(dividerID, name string) error
	ReopenSession(sessionID string) error
	ResizeTerm(connectionKey string, cols, rows int)
	RespondToNotch(response NotchResponse) error
	ResumeSession(sessionID string) error
	RetryQueuedMessage(sessionID, messageID string) error
	ReviewPreview(sessionID string) (string, error)
	SaveImage(dataB64 string) (string, error)
	SaveSessionAutomation(sessionID, automationID, name, instructions string, everyMinutes int, nextRunAt string, enabled bool) (core.SessionAutomation, error)
	SearchTranscripts(query string) (SearchResult, error)
	SendMessage(sessionID, text string) error
	SendReview(sessionID string) error
	SendSkill(sessionID, cmd string) error
	SentReviews(sessionID string) ([]core.SessionReview, error)
	SessionAutomation(sessionID string) (*core.SessionAutomation, error)
	SessionConversation(sessionID string) ConversationItemsResult
	SessionLinks(sessionID string) (SessionLinksResult, error)
	SessionPreview(sessionID string) SessionPreviewResult
	SetActiveTerm(sessionID string)
	SetBreakConfig(c core.BreakConfig) error
	SetDividerCollapsed(dividerID string, collapsed bool) error
	SetMainBranch(projectID, main string) error
	SetNotificationsEnabled(on bool) error
	SetSessionService(sessionID string, service bool) error
	ShowNotchEvent(event NotchEvent) error
	SnoozeBreak() error
	StartBoardItem(projectID, token string) (string, error)
	Stats(days int) (core.Stats, error)
	StructuredDiff(projectID, reference, mode string) (core.StructuredDiff, error)
	SwitchSessionVendor(sessionID, vendor string, includeHistory bool) error
	TakeBreak() error
	Timeline() (TimelineResult, error)
	WatchConversation(sessionID string)
	WorktreeDiff(projectID, reference string) (string, error)
	WriteTerm(connectionKey, dataB64 string)
	Zeitgeist() core.ZgInfo
	ZeitgeistPause() error
	ZeitgeistResume() error
	ZeitgeistStart(ref string) (core.ZgProject, error)
	ZeitgeistStop(note string) (core.ZgStopped, error)
}

// Die bestehende *App erfüllt die Nahtstelle als lokale Implementierung, ohne
// dass sich ihr Verhalten ändert.
var _ HostAPI = (*App)(nil)
