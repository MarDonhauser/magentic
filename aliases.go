package main

import "magentic/core"

type (
	State          = core.State
	Project        = core.Project
	Agent          = core.Agent
	RegistryChange = core.RegistryChange
	AgentStatus    = core.AgentStatus
	UsageInfo      = core.UsageInfo
	Overview       = core.Overview
	ZgInfo         = core.ZgInfo
)

const (
	StatusUnknown = core.StatusUnknown
	StatusRunning = core.StatusRunning
	StatusAgents  = core.StatusAgents
	StatusShell   = core.StatusShell
	StatusBlocked = core.StatusBlocked
	StatusIdle    = core.StatusIdle
	StatusExited  = core.StatusExited
	StatusDead    = core.StatusDead
	StatusTerm    = core.StatusTerm

	KindTerm = core.KindTerm
)

var (
	sessionPrefix = core.SessionPrefix

	tmux                 = core.Tmux
	tmuxSessionName      = core.SessionName
	targetSession        = core.TargetSession
	targetPane           = core.TargetPane
	TmuxHasSession       = core.TmuxHasSession

	LoadState             = core.LoadState
	StatePath             = core.StatePath
	OpenRegistry          = core.OpenRegistry
	RegisterProject       = core.RegisterProject
	RemoveProject         = core.RemoveProject
	AddDiscoveredSessions = core.AddDiscoveredSessions

	lastLines            = core.LastLines
	statusRank           = core.StatusRank
	notifyDesktop        = core.NotifyDesktop
	backgroundAgentCount = core.BackgroundAgentCount
	agentsDetail         = core.AgentsDetail
	backgroundShellCount = core.BackgroundShellCount
	shellDetail          = core.ShellDetail

	CachedUsage   = core.CachedUsage
	PickAgentName = core.PickAgentName

	zeitgeistInfo   = core.ZeitgeistInfo
	zeitgeistStart  = core.ZeitgeistStart
	zeitgeistPause  = core.ZeitgeistPause
	zeitgeistResume = core.ZeitgeistResume
	zeitgeistStop   = core.ZeitgeistStop
	formatEuro      = core.FormatEuro
	formatDurShort  = core.FormatDurShort

	discoverNew            = core.DiscoverNew
	createAgentSession     = core.CreateAgentSession
	createTermSession      = core.CreateTermSession
	createTermSessionForID = core.CreateTermSessionForID
	removeSession          = core.RemoveRegisteredSession
	startSkillAgent        = core.StartSkillAgent
	sendSkillByID          = core.SendSkillByID
	observeSessions        = core.Observe

	shortPath     = core.ShortPath
	formatAge     = core.FormatAge
	formatAgeWord = core.FormatAgeWord
	shortWeekday  = core.ShortWeekday
	sanitizeName  = core.SanitizeName
	envWithout    = core.EnvWithout
)
