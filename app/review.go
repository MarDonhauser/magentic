package main

import (
	"fmt"
	"strings"
	"time"

	"magentic/core"
)

// StructuredDiff returns the structured file/hunk/line diff for a ProjectID
// plus opaque WorktreeRef and comparison mode ("working_tree" or "branch").
// The Worktree is resolved freshly at action time; an unresolvable Worktree
// yields an error rather than an empty diff, and unavailable Git knowledge
// names its failing operation instead of rendering as no changes.
func (a *App) StructuredDiff(projectID, reference, mode string) (core.StructuredDiff, error) {
	diffMode := core.DiffComparisonWorkingTree
	switch strings.TrimSpace(mode) {
	case "", string(core.DiffComparisonWorkingTree):
	case string(core.DiffComparisonBranch):
		diffMode = core.DiffComparisonBranch
	default:
		return core.StructuredDiff{}, fmt.Errorf("unbekannter Vergleichsmodus %q", mode)
	}
	_, target, err := resolveWorktreeTarget(a.ctx, projectID, reference)
	if err != nil {
		return core.StructuredDiff{}, err
	}
	fact := core.NewRepositories().StructuredDiff(a.ctx, target, diffMode)
	if !fact.Known() {
		message := "Strukturierter Diff ist derzeit nicht verfügbar"
		if fact.Problem != nil {
			operation := strings.TrimSpace(fact.Problem.Operation)
			detail := strings.TrimSpace(fact.Problem.Message)
			switch {
			case operation != "" && detail != "":
				message = operation + ": " + detail
			case detail != "":
				message = detail
			case operation != "":
				message = operation
			}
		}
		return core.StructuredDiff{}, fmt.Errorf("%s", message)
	}
	return fact.Value, nil
}

func parseReviewComparisonMode(mode string) (core.DiffComparisonMode, error) {
	switch strings.TrimSpace(mode) {
	case "", string(core.DiffComparisonWorkingTree):
		return core.DiffComparisonWorkingTree, nil
	case string(core.DiffComparisonBranch):
		return core.DiffComparisonBranch, nil
	default:
		return "", fmt.Errorf("unbekannter Vergleichsmodus %q", mode)
	}
}

// AddReviewComment attaches a comment to a diff line or line range of the
// Session's open Review and returns the stored comment.
func (a *App) AddReviewComment(sessionID, path string, oldStart, oldEnd, newStart, newEnd int, quoted, text, mode string) (core.ReviewComment, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return core.ReviewComment{}, err
	}
	diffMode, err := parseReviewComparisonMode(mode)
	if err != nil {
		return core.ReviewComment{}, err
	}
	comment := core.ReviewComment{
		ID: core.NewUUID(), Path: path,
		OldStart: oldStart, OldEnd: oldEnd, NewStart: newStart, NewEnd: newEnd,
		Quoted: quoted, Text: text, Mode: diffMode, CreatedAt: time.Now(),
	}
	if _, err := core.OpenRegistry(core.StatePath()).Change(a.ctx,
		core.AddReviewComment(session.ID, session.Name, comment)); err != nil {
		return core.ReviewComment{}, err
	}
	comment.Path = strings.TrimSpace(comment.Path)
	comment.Text = strings.TrimSpace(comment.Text)
	comment.LineRef = core.ReviewLineRef(comment)
	return comment, nil
}

// EditReviewComment replaces the text of one comment of the open Review.
func (a *App) EditReviewComment(sessionID, commentID, text string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx,
		core.EditReviewComment(session.ID, session.Name, commentID, text))
	return err
}

// DeleteReviewComment removes one comment from the open Review.
func (a *App) DeleteReviewComment(sessionID, commentID string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx,
		core.DeleteReviewComment(session.ID, session.Name, commentID))
	return err
}

// DiscardSentReview removes one sent Review from the history. The open Review
// is unaffected.
func (a *App) DiscardSentReview(sessionID, reviewID string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx,
		core.DiscardSentReview(session.ID, session.Name, reviewID))
	return err
}

// OpenReview returns the Session's open Review, or nil when the Session has
// none yet. A nil Review reads as an empty open Review.
func (a *App) OpenReview(sessionID string) (*core.SessionReview, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return nil, err
	}
	if session.Review == nil {
		return nil, nil
	}
	review := core.ReviewForReading(*session.Review)
	return &review, nil
}

// SentReviews returns the retained sent Reviews of a Session, oldest first.
func (a *App) SentReviews(sessionID string) ([]core.SessionReview, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return nil, err
	}
	sent := make([]core.SessionReview, 0, len(session.SentReviews))
	for _, review := range session.SentReviews {
		sent = append(sent, core.ReviewForReading(review))
	}
	return sent, nil
}

// ReviewPreview renders the open Review into the prompt that SendReview would
// deliver, so the desktop shows it before sending.
func (a *App) ReviewPreview(sessionID string) (string, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return "", err
	}
	if session.Review == nil || len(session.Review.Comments) == 0 {
		return "", fmt.Errorf("Review enthält keine Kommentare")
	}
	return core.RenderReviewPrompt(*session.Review, session.Name), nil
}

// SendReview delivers the open Review to the Session's agent as one prompt
// through the durable queued-message path. An empty Review is refused.
func (a *App) SendReview(sessionID string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	return core.SendSessionReview(session.ID, a.observeSessions)
}
