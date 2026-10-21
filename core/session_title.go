package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// SessionDigest is what a Session's own record says about its topic and its
// latest result.
type SessionDigest struct {
	FirstPrompt string
	LastResult  Item
}

// DigestConversation picks the developer's first prompt and the agent's
// latest message. Delegated work is left out: a subagent's prompt is not the
// developer's topic, and its answer is not the Session's result.
func DigestConversation(conversation Conversation) SessionDigest {
	var digest SessionDigest
	for _, item := range conversation.Items {
		if item.Delegated || item.InProgress {
			continue
		}
		switch {
		case item.Kind == ItemKindDeveloperPrompt && digest.FirstPrompt == "":
			digest.FirstPrompt = strings.TrimSpace(item.Title + "\n" + item.Detail)
		case item.Role == ItemRoleAgent && item.Kind == ItemKindAgentMessage && item.OccurredAt.After(digest.LastResult.OccurredAt):
			digest.LastResult = item
		}
	}
	return digest
}

const (
	sessionTitleMaxRunes  = 40
	sessionTitlePromptCap = 1500
)

const sessionTitleInstructions = "Du benennst Arbeitssitzungen eines Coding-Agents nach ihrem Thema. " +
	"Du bekommst die erste Anfrage der Sitzung. Beantworte sie nicht und stelle keine Rückfragen. " +
	"Antworte ausschließlich mit einem Titel aus 2 bis 4 Wörtern in der Sprache der Anfrage, " +
	"ohne Anführungszeichen, ohne Satzzeichen am Ende, ohne weitere Zeilen."

// GenerateSessionTitle asks a small model for a topic, the way Claude Code
// titles its own conversations. Magentic starts Claude with --name, which
// suppresses that title, so it has to ask for one itself. The call runs
// without hooks, tools, MCP servers or a persisted conversation so it leaves
// no trace in the developer's history.
func GenerateSessionTitle(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("kein Prompt für einen Titel")
	}
	if runes := []rune(prompt); len(runes) > sessionTitlePromptCap {
		prompt = string(runes[:sessionTitlePromptCap])
	}
	cmd := exec.CommandContext(ctx, "claude", "-p",
		"--model", "haiku",
		"--no-session-persistence",
		"--tools", "",
		"--strict-mcp-config",
		"--settings", `{"disableAllHooks":true}`,
		"--system-prompt", sessionTitleInstructions,
	)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader("Erste Anfrage der Sitzung:\n<anfrage>\n" + prompt + "\n</anfrage>\nTitel:")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("Titel-Erzeugung fehlgeschlagen: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("Titel-Erzeugung fehlgeschlagen: %w", err)
	}
	title := CleanSessionTitle(string(out))
	if title == "" {
		return "", errors.New("das Modell lieferte keinen Titel")
	}
	return title, nil
}

// CleanSessionTitle keeps the first line of a model's answer and strips the
// decoration a model tends to add anyway: Markdown, quotes, a "Titel:" label
// and closing punctuation.
func CleanSessionTitle(raw string) string {
	line := ""
	for _, candidate := range strings.Split(raw, "\n") {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			line = candidate
			break
		}
	}
	line = strings.TrimLeft(line, "#*-> ")
	for _, label := range []string{"Titel:", "Title:"} {
		if len(line) >= len(label) && strings.EqualFold(line[:len(label)], label) {
			line = line[len(label):]
		}
	}
	line = strings.Join(strings.Fields(line), " ")
	quote := func(r rune) bool { return strings.ContainsRune(`"'„“”‚‘’«»*_`+"`", r) }
	line = strings.TrimLeftFunc(line, func(r rune) bool { return unicode.IsSpace(r) || quote(r) })
	line = strings.TrimRightFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || quote(r) || strings.ContainsRune(".!?:;,", r)
	})
	runes := []rune(line)
	if len(runes) <= sessionTitleMaxRunes {
		return line
	}
	cut := string(runes[:sessionTitleMaxRunes])
	if space := strings.LastIndex(cut, " "); space > 0 {
		cut = cut[:space]
	}
	return strings.TrimRightFunc(cut, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".!?:;,-", r)
	})
}
