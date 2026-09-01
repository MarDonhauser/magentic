package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"sync"

	"magentic/core"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	notchEventName    = "notch://event"
	notchClearName    = "notch://clear"
	notchResponseName = "notch://response"
)

var (
	notchOwnerMu sync.RWMutex
	notchOwner   *App
)

func installNotchOwner(app *App) {
	notchOwnerMu.Lock()
	notchOwner = app
	notchOwnerMu.Unlock()
}

func dispatchNotchResponse(payload string) {
	response, err := notchResponseFromJSON(payload)
	if err != nil {
		core.Logf("notch: ungültige Antwort: %v", err)
		return
	}
	notchOwnerMu.RLock()
	app := notchOwner
	notchOwnerMu.RUnlock()
	if app == nil {
		return
	}
	if err := app.RespondToNotch(response); err != nil {
		core.Logf("notch: Antwort konnte nicht angewendet werden: %v", err)
	}
}

type NotchOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

type NotchEvent struct {
	ID        string        `json:"id"`
	Kind      string        `json:"kind"`
	Title     string        `json:"title"`
	Detail    string        `json:"detail,omitempty"`
	Options   []NotchOption `json:"options"`
	SessionID string        `json:"sessionId,omitempty"`
}

type NotchResponse struct {
	ID       string `json:"id"`
	OptionID string `json:"optionId"`
}

var notchAssetPattern = regexp.MustCompile(`(?s)<script([^>]*?)src="([^"]+)"([^>]*)></script>|<link([^>]*?)href="([^"]+)"([^>]*)>`)

func notchDocumentFromAssets(assetsFS fs.FS) (string, error) {
	document, err := fs.ReadFile(assetsFS, "frontend/dist/notch.html")
	if err != nil {
		return "", err
	}
	var inlineErr error
	inlined := notchAssetPattern.ReplaceAllStringFunc(string(document), func(tag string) string {
		if inlineErr != nil {
			return tag
		}
		matches := notchAssetPattern.FindStringSubmatch(tag)
		assetPath := matches[2]
		isScript := assetPath != ""
		if !isScript {
			assetPath = matches[5]
			if !strings.Contains(strings.ToLower(tag), "stylesheet") {
				return tag
			}
		}
		assetPath = strings.TrimPrefix(assetPath, "/")
		assetPath = path.Clean(assetPath)
		if assetPath == "." || strings.HasPrefix(assetPath, "../") {
			inlineErr = fmt.Errorf("ungültiger Notch-Asset-Pfad %q", assetPath)
			return tag
		}
		contents, readErr := fs.ReadFile(assetsFS, "frontend/dist/"+assetPath)
		if readErr != nil {
			inlineErr = readErr
			return tag
		}
		if isScript {
			script := strings.ReplaceAll(string(contents), "</script", "<\\/script")
			return "<script type=\"module\">" + script + "</script>"
		}
		return "<style>" + string(contents) + "</style>"
	})
	if inlineErr != nil {
		return "", inlineErr
	}
	return inlined, nil
}

func validateNotchEvent(event NotchEvent) error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Title) == "" {
		return fmt.Errorf("Notch-Event braucht ID und Titel")
	}
	switch event.Kind {
	case "permission", "question", "review":
	default:
		return fmt.Errorf("unbekannte Notch-Art %q", event.Kind)
	}
	if len(event.Options) == 0 {
		return fmt.Errorf("Notch-Event %q hat keine Optionen", event.ID)
	}
	seen := make(map[string]bool, len(event.Options))
	for _, option := range event.Options {
		if strings.TrimSpace(option.ID) == "" || strings.TrimSpace(option.Label) == "" || seen[option.ID] {
			return fmt.Errorf("Notch-Event %q hat eine ungültige Option", event.ID)
		}
		seen[option.ID] = true
		switch option.Tone {
		case "", "allow", "deny", "neutral":
		default:
			return fmt.Errorf("unbekannter Notch-Tone %q", option.Tone)
		}
	}
	return nil
}

func (a *App) ShowNotchEvent(event NotchEvent) error {
	if err := validateNotchEvent(event); err != nil {
		return err
	}
	a.notchMu.Lock()
	copyOfEvent := event
	copyOfEvent.Options = append([]NotchOption(nil), event.Options...)
	a.notchEvent = &copyOfEvent
	a.notchMu.Unlock()

	if err := nativeShowNotchEvent(copyOfEvent); err != nil {
		return err
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, notchEventName, copyOfEvent)
	}
	return nil
}

func (a *App) ClearNotch(id string) error {
	a.notchMu.Lock()
	if id != "" && a.notchEvent != nil && a.notchEvent.ID != id {
		a.notchMu.Unlock()
		return nil
	}
	a.notchEvent = nil
	a.notchMu.Unlock()

	if err := nativeClearNotch(id); err != nil {
		return err
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, notchClearName, map[string]string{"id": id})
	}
	return nil
}

func (a *App) RespondToNotch(response NotchResponse) error {
	a.notchMu.Lock()
	if a.notchEvent == nil || a.notchEvent.ID != response.ID {
		a.notchMu.Unlock()
		return fmt.Errorf("Notch-Event %q ist nicht mehr aktiv", response.ID)
	}
	event := *a.notchEvent
	event.Options = append([]NotchOption(nil), a.notchEvent.Options...)
	a.notchMu.Unlock()

	validOption := false
	for _, option := range event.Options {
		if option.ID == response.OptionID {
			validOption = true
			break
		}
	}
	if !validOption {
		return fmt.Errorf("Notch-Option %q gehört nicht zu Event %q", response.OptionID, response.ID)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, notchResponseName, response)
	}
	a.notchMu.Lock()
	if a.notchEvent != nil && a.notchEvent.ID == event.ID {
		a.notchEvent = nil
	}
	a.notchMu.Unlock()
	nativeAcknowledgeNotch(event.ID)

	var actionErr error
	switch response.OptionID {
	case "allow", "deny":
		actionErr = a.answerPermissionEvent(event, response.OptionID)
	case "open":
		actionErr = a.openNotchSession(event.SessionID)
	case "later":
		// Explicitly acknowledged without changing the underlying Session.
	default:
		actionErr = fmt.Errorf("Notch-Option %q hat keine angebundene Aktion", response.OptionID)
	}
	if actionErr != nil && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "notch://error", actionErr.Error())
	}
	return actionErr
}

func (a *App) answerPermissionEvent(event NotchEvent, optionID string) error {
	if event.Kind != "permission" || strings.TrimSpace(event.SessionID) == "" {
		return fmt.Errorf("Event ist keine direkte Session-Freigabe")
	}
	state, session, err := loadSessionByID(event.SessionID)
	if err != nil {
		return err
	}
	snapshot := a.observationFor(state.Agents, true)
	var observed *core.SessionObservation
	for i := range snapshot.Sessions {
		if snapshot.Sessions[i].SessionID == session.ID {
			observed = &snapshot.Sessions[i]
			break
		}
	}
	if observed == nil || observed.Attention != core.AttentionNeedsInput || observed.Detail == "" {
		return fmt.Errorf("Freigabe ist nicht mehr aktuell; Session wird nicht automatisch bedient")
	}
	keys := []string{"send-keys", "-t", core.TargetPane(session.TmuxName())}
	if optionID == "deny" {
		keys = append(keys, "Escape")
	} else {
		// Permission menus put the least-persistent allow choice first. Repeated
		// Up makes that choice deterministic even if the terminal selection was
		// moved after the Notch event was emitted.
		keys = append(keys, "Up", "Up", "Up", "Up", "Up", "Up", "Enter")
	}
	if _, err := core.Tmux(keys...); err != nil {
		return fmt.Errorf("Antwort an %s fehlgeschlagen: %w", session.Name, err)
	}
	return nil
}

func (a *App) openNotchSession(rawID string) error {
	_, session, err := loadSessionByID(rawID)
	if err != nil {
		return err
	}
	if a.ctx == nil {
		return fmt.Errorf("Hauptfenster ist noch nicht bereit")
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
	bringToFront()
	runtime.EventsEmit(a.ctx, "notch://open-session", map[string]string{
		"sessionId": string(session.ID),
		"name":      session.Name,
	})
	return nil
}

func (a *App) syncNotch(plan core.AttentionPlan, snapshot core.ObservationSnapshot) {
	a.clearStaleNotch(snapshot)
	for _, notification := range plan.Notifications {
		event, ok := notchEventForAttention(notification, snapshot)
		if !ok {
			continue
		}
		if err := a.ShowNotchEvent(event); err != nil {
			core.Logf("notch: Attention-Event konnte nicht gezeigt werden: %v", err)
		}
		return
	}
}

func (a *App) clearStaleNotch(snapshot core.ObservationSnapshot) {
	a.notchMu.Lock()
	if a.notchEvent == nil || a.notchEvent.SessionID == "" {
		a.notchMu.Unlock()
		return
	}
	event := *a.notchEvent
	a.notchMu.Unlock()

	active := false
	for _, observed := range snapshot.Sessions {
		if string(observed.SessionID) != event.SessionID {
			continue
		}
		if event.Kind == "review" {
			active = observed.Attention == core.AttentionReview
		} else {
			active = observed.Attention == core.AttentionNeedsInput
		}
		break
	}
	if !active {
		_ = a.ClearNotch(event.ID)
	}
}

func notchEventForAttention(notification core.AttentionNotificationIntent, snapshot core.ObservationSnapshot) (NotchEvent, bool) {
	if notification.Kind != core.AttentionIntentNeedsInput && notification.Kind != core.AttentionIntentSessionComplete {
		return NotchEvent{}, false
	}
	label := strings.TrimSpace(strings.TrimPrefix(notification.Title, "magentic ·"))
	if label == "" {
		label = string(notification.SessionID)
	}
	event := NotchEvent{ID: notification.DedupeKey, SessionID: string(notification.SessionID)}
	if notification.Kind == core.AttentionIntentSessionComplete {
		event.Kind = "review"
		event.Title = label + " ist bereit zur Review"
		event.Detail = notification.Message
		event.Options = []NotchOption{
			{ID: "later", Label: "Später", Tone: "neutral"},
			{ID: "open", Label: "Review öffnen", Tone: "allow"},
		}
		return event, true
	}

	var detail string
	for _, observed := range snapshot.Sessions {
		if observed.SessionID == notification.SessionID {
			detail = observed.Detail
			break
		}
	}
	if detail != "" {
		event.Kind = "permission"
		event.Title = label + " braucht eine Freigabe"
		event.Detail = detail + " – direkt entscheiden oder die Session öffnen."
		event.Options = []NotchOption{
			{ID: "deny", Label: "Ablehnen", Tone: "deny"},
			{ID: "open", Label: "Session öffnen", Tone: "neutral"},
			{ID: "allow", Label: "Erlauben", Tone: "allow"},
		}
		return event, true
	}

	event.Kind = "question"
	event.Title = label + " wartet auf deine Antwort"
	event.Detail = "Die Rückfrage ist im Terminal geöffnet."
	event.Options = []NotchOption{
		{ID: "later", Label: "Später", Tone: "neutral"},
		{ID: "open", Label: "Session öffnen", Tone: "allow"},
	}
	return event, true
}

func notchResponseFromJSON(payload string) (NotchResponse, error) {
	var response NotchResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return NotchResponse{}, err
	}
	if response.ID == "" || response.OptionID == "" {
		return NotchResponse{}, fmt.Errorf("unvollständige Notch-Antwort")
	}
	return response, nil
}
