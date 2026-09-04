package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReviewLineRef states the anchored lines of a comment in interface copy: a
// single line reads as one Zeile, a range as Zeilen. Anchors on the removed
// side name the removal explicitly, since their numbers predate the change.
func ReviewLineRef(comment ReviewComment) string {
	start, end := comment.NewStart, comment.NewEnd
	removed := false
	if start == 0 && end == 0 {
		start, end = comment.OldStart, comment.OldEnd
		removed = true
	}
	if end < start || end == 0 {
		end = start
	}
	var ref string
	if end == start {
		ref = fmt.Sprintf("Zeile %d", start)
	} else {
		ref = fmt.Sprintf("Zeilen %d–%d", start, end)
	}
	if removed {
		if end == start {
			return ref + " (entfernte Zeile)"
		}
		return ref + " (entfernte Zeilen)"
	}
	return ref
}

// RenderReviewPrompt renders an open Review into the one plain-text prompt
// that is delivered to the Session's agent: per comment the file path, the
// line reference, the quoted code fenced, the comment text and the comparison
// mode, in the Review's file-then-line order.
func RenderReviewPrompt(review SessionReview, sessionName string) string {
	comments := append([]ReviewComment(nil), review.Comments...)
	sortReviewComments(comments)
	name := strings.TrimSpace(sessionName)
	if name == "" {
		name = "(unbenannte Session)"
	}
	var out strings.Builder
	if len(comments) == 1 {
		fmt.Fprintf(&out, "Code-Review für die Session %q mit einem Kommentar. Gehe den Kommentar durch und setze ihn um.\n", name)
	} else {
		fmt.Fprintf(&out, "Code-Review für die Session %q mit %d Kommentaren. Gehe jeden Kommentar der Reihe nach durch und setze ihn um.\n", name, len(comments))
	}
	const fence = "```"
	for index, comment := range comments {
		fmt.Fprintf(&out, "\nKommentar %d von %d — Datei %q (%s, %s)\n%s\n%s\n%s\n%s\n",
			index+1, len(comments), comment.Path,
			ReviewLineRef(comment), DiffComparisonModeLabel(comment.Mode),
			fence, comment.Quoted, fence, comment.Text)
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

// SendSessionReview renders the Session's open Review into one prompt and
// delivers it through the durable queued-message path, so a busy agent
// receives it as soon as it is input-ready. The Review is marked sent only
// after the queue has accepted the prompt; any earlier failure leaves the
// open Review untouched so it can be sent again.
func SendSessionReview(id SessionID, observe func(context.Context, []Session) ObservationSnapshot) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	session := st.SessionByID(id)
	if session == nil {
		return fmt.Errorf("unbekannte SessionID: %s", id)
	}
	if session.Review == nil || len(session.Review.Comments) == 0 {
		return fmt.Errorf("Review enthält keine Kommentare")
	}
	prompt := RenderReviewPrompt(*session.Review, session.Name)
	if err := SendQueuedMessageWithObserver(session.ID, QueuedMessageKindReview, prompt, observe); err != nil {
		return err
	}
	_, err = OpenRegistry(StatePath()).Change(context.Background(), MarkReviewSent(session.ID, session.Name, time.Now()))
	return err
}
