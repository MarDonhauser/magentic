package main

import "magentic/core"

type (
	State          = core.State
	Project        = core.Project
	Agent          = core.Agent
	Todo           = core.Todo
	AgentStatus    = core.AgentStatus
	GitInfo        = core.GitInfo
	WorktreeInfo   = core.WorktreeInfo
	SessionChanges = core.SessionChanges
	PaneInfo       = core.PaneInfo
	UsageInfo      = core.UsageInfo
	Overview       = core.Overview
)

const (
	StatusUnknown = core.StatusUnknown
	StatusRunning = core.StatusRunning
	StatusAgents  = core.StatusAgents
	StatusBlocked = core.StatusBlocked
	StatusIdle    = core.StatusIdle
	StatusExited  = core.StatusExited
	StatusDead    = core.StatusDead
)

var (
	sessionPrefix = core.SessionPrefix

	tmux                 = core.Tmux
	tmuxSessionName      = core.SessionName
	targetSession        = core.TargetSession
	targetPane           = core.TargetPane
	TmuxHasSession       = core.TmuxHasSession
	TmuxNewClaudeSession = core.TmuxNewClaudeSession
	TmuxKillSession      = core.TmuxKillSession
	TmuxCapturePane      = core.TmuxCapturePane
	TmuxPaneInfos        = core.TmuxPaneInfos
	TmuxListSessions     = core.TmuxListSessions

	LoadState = core.LoadState

	CollectStatuses      = core.CollectStatuses
	DetectClaudeStatus   = core.DetectClaudeStatus
	lastLines            = core.LastLines
	statusRank           = core.StatusRank
	notifyDesktop        = core.NotifyDesktop
	backgroundAgentCount = core.BackgroundAgentCount
	agentsDetail         = core.AgentsDetail

	gitCmd                = core.GitCmd
	CollectGitInfo        = core.CollectGitInfo
	CollectWorktrees      = core.CollectWorktrees
	CollectSessionChanges = core.CollectSessionChanges
	CaptureBaseline       = core.CaptureBaseline
	AheadBehind           = core.AheadBehind
	CreateWorktree        = core.CreateWorktree

	CachedUsage   = core.CachedUsage
	PickAgentName = core.PickAgentName
	BuildOverview = core.BuildOverview

	discoverNew         = core.DiscoverNew
	startSkillAgent     = core.StartSkillAgent
	sendPromptWhenReady = core.SendPromptWhenReady
	sendSlashCommand    = core.SendSlashCommand

	shortPath     = core.ShortPath
	formatAge     = core.FormatAge
	formatAgeWord = core.FormatAgeWord
	shortWeekday  = core.ShortWeekday
	sanitizeName  = core.SanitizeName
	envWithout    = core.EnvWithout
)
