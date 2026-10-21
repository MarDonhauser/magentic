package main

import (
	"context"
	"os/exec"
	"time"

	"magentic/core"
)

const (
	sessionDigestInterval = 20 * time.Second
	// A Session whose record holds no prompt yet is looked at again on the
	// next pass: it is new, its record is small, and its title should appear
	// soon after the first prompt.
	sessionDigestEmptyRetry = sessionDigestInterval
	sessionDigestFailRetry  = 10 * time.Minute
	sessionTitleTimeout     = 90 * time.Second
)

// sessionDigestLoop titles Sessions after their first prompt and fills in the
// time of the latest result for Sessions recorded before Magentic tracked it.
// It works one Session at a time; a title costs one small model call.
func (a *App) sessionDigestLoop() {
	_, lookErr := exec.LookPath("claude")
	canTitle := lookErr == nil
	retryAt := map[core.SessionID]time.Time{}
	for {
		time.Sleep(sessionDigestInterval)
		st, err := core.LoadState()
		if err != nil {
			continue
		}
		for _, session := range st.Agents {
			needsTitle := canTitle && session.Title == ""
			if session.IsTerm() || session.IsDock() || (!needsTitle && !session.ResultAt.IsZero()) {
				continue
			}
			if time.Now().Before(retryAt[session.ID]) {
				continue
			}
			retryAt[session.ID] = time.Now().Add(digestSession(session, needsTitle))
		}
	}
}

// digestSession reads one Session's record and stores what it learned. It
// returns how long to wait before looking at this Session again.
func digestSession(session core.Session, needsTitle bool) time.Duration {
	reading := core.NewConversationReader().Read(session)
	if reading.Availability != core.ConversationAvailable || reading.Conversation == nil {
		return sessionDigestFailRetry
	}
	digest := core.DigestConversation(*reading.Conversation)
	registry := core.OpenRegistry(core.StatePath())
	if session.ResultAt.IsZero() && !digest.LastResult.OccurredAt.IsZero() {
		if _, err := registry.Change(context.Background(), core.RecordSessionResult(session.ID, session.Name, digest.LastResult.OccurredAt)); err != nil {
			core.Logf("digest: Ergebniszeit für %s nicht gespeichert: %v", session.Name, err)
		}
	}
	if !needsTitle {
		return sessionDigestFailRetry
	}
	if digest.FirstPrompt == "" {
		if time.Since(session.CreatedAt) < time.Hour {
			return sessionDigestEmptyRetry
		}
		return sessionDigestFailRetry
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionTitleTimeout)
	defer cancel()
	title, err := core.GenerateSessionTitle(ctx, digest.FirstPrompt)
	if err != nil {
		core.Logf("digest: kein Titel für %s: %v", session.Name, err)
		return sessionDigestFailRetry
	}
	if _, err := registry.Change(context.Background(), core.SetSessionTitle(session.ID, session.Name, title)); err != nil {
		core.Logf("digest: Titel für %s nicht gespeichert: %v", session.Name, err)
		return sessionDigestFailRetry
	}
	return 0
}
