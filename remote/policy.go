package remote

import (
	"fmt"
	"sort"
)

// ActionClass teilt jede Host-API-Methode in genau zwei Klassen. Erzwungen
// wird ausschließlich hostseitig vor jeder Nebenwirkung (D8); was der Client
// anzeigt, ist nur Anzeige.
type ActionClass string

const (
	// ActionPermitted darf ein Client über das Netz aufrufen.
	ActionPermitted ActionClass = "permitted"
	// ActionRestricted verweigert der Host ohne explizites Opt-in des
	// Betreibers. Dazu gehören destruktive und hostförmige Operationen,
	// deren Folge ein Client ohne Blick aufs Host-Dateisystem nicht
	// beurteilen kann, sowie rein lokale UI-Methoden, die der Client
	// grundsätzlich selbst bedient statt sie über das Netz zu schicken.
	ActionRestricted ActionClass = "restricted"
)

// PolicyEntry ist eine klassifizierte Methode mit dem Grund, den der Client
// anzeigt und den der Host in seiner Verweigerung nennt.
type PolicyEntry struct {
	Class  ActionClass `json:"class"`
	Reason string      `json:"reason"`
}

// RemoteActionPolicy klassifiziert jede Methode aus HostAPIMethods. Die
// Aufteilung folgt dem Spec: Beobachten, Terminal-Attach, Eingaben,
// Nachrichten/Skills/Prompts, Session-Anlage, Umbenennen, Gesehen-Markieren
// und host-aufgelöste Specification-Starts sind erlaubt; Worktree-/Project-
// Entfernung, Project-Registrierung per Pfad, Session-Kill und alles, was
// einen Client-Pfad trägt, ist beschränkt.
var RemoteActionPolicy = map[string]PolicyEntry{
	// Beobachten: erlaubt.
	"Overview":            {ActionPermitted, "Beobachtung"},
	"Inbox":               {ActionPermitted, "Beobachtung"},
	"GitGraph":            {ActionPermitted, "Beobachtung"},
	"Board":               {ActionPermitted, "Beobachtung"},
	"BoardArchive":        {ActionPermitted, "Beobachtung"},
	"Stats":               {ActionPermitted, "Beobachtung"},
	"SessionAutomation":   {ActionPermitted, "Beobachtung"},
	"CompleteCommands":    {ActionPermitted, "Beobachtung"},
	"PromptLinePattern":   {ActionPermitted, "Beobachtung"},
	"SessionConversation": {ActionPermitted, "Beobachtung"},
	"SessionPreview":      {ActionPermitted, "Beobachtung"},
	"SessionLinks":        {ActionPermitted, "Beobachtung"},
	"SearchTranscripts":   {ActionPermitted, "Beobachtung"},
	"Timeline":            {ActionPermitted, "Beobachtung"},
	"WorktreeDiff":        {ActionPermitted, "Beobachtung"},
	"StructuredDiff":      {ActionPermitted, "Beobachtung"},
	"ReviewPreview":       {ActionPermitted, "Beobachtung"},
	"OpenReview":          {ActionPermitted, "Beobachtung"},
	"SentReviews":         {ActionPermitted, "Beobachtung"},
	"DeployStatus":        {ActionPermitted, "Beobachtung"},
	"Breaks":              {ActionPermitted, "Beobachtung"},
	"BreakConfig":         {ActionPermitted, "Beobachtung"},
	"AgentVendors":        {ActionPermitted, "Beobachtung"},

	// Terminal über den Stream-Kanal: erlaubt.
	"OpenTerm":   {ActionPermitted, "Terminal-Anhang über den Stream-Kanal"},
	"WriteTerm":  {ActionPermitted, "Terminal-Eingabe über den Stream-Kanal"},
	"ResizeTerm": {ActionPermitted, "Terminal-Größe über den Stream-Kanal"},
	"CloseTerm":  {ActionPermitted, "Terminal-Anhang über den Stream-Kanal"},

	// Nachrichten, Skills, Prompts, Session-Anlage, harmlose Registry-
	// Übergänge: erlaubt.
	"SendSkill":             {ActionPermitted, "Nachricht an die Session"},
	"SendMessage":           {ActionPermitted, "Nachricht an die Session"},
	"DiscardQueuedMessage":  {ActionPermitted, "Outbox-Verwaltung der Session"},
	"RetryQueuedMessage":    {ActionPermitted, "Outbox-Verwaltung der Session"},
	"SaveSessionAutomation": {ActionPermitted, "Automatisierung der Session"},
	"DeleteSessionAutomation": {ActionPermitted, "Automatisierung der Session"},
	"SwitchSessionVendor":     {ActionPermitted, "Vendor-Wechsel der Session"},
	"HandoffSession":          {ActionPermitted, "Übergabe zwischen Sessions"},
	"DoneAgent":               {ActionPermitted, "Nachricht an die Session"},
	"NewSession":              {ActionPermitted, "Session-Anlage in registriertem Project"},
	"NewSessionWithVendor":    {ActionPermitted, "Session-Anlage in registriertem Project"},
	"NewTermSession":          {ActionPermitted, "Session-Anlage in registriertem Project"},
	"NewTermSessionFor":       {ActionPermitted, "Session-Anlage in registriertem Project"},
	"NewDockSession":          {ActionPermitted, "Session-Anlage in registriertem Project"},
	"MigrateDockSessions":     {ActionPermitted, "Session-Anlage in registriertem Project"},
	"MarkSeen":                {ActionPermitted, "Gesehen-Markierung"},
	"SetSessionService":       {ActionPermitted, "Service-Markierung"},
	"StartBoardItem":          {ActionPermitted, "Specification-Start, host-aufgelöst"},
	"Cleanup":                 {ActionPermitted, "Cleanup-Session, host-aufgelöst"},
	"Merge":                   {ActionPermitted, "Merge-Session, host-aufgelöst"},
	"Deploy":                  {ActionPermitted, "Deploy-Session, host-aufgelöst"},
	"LaterSession":            {ActionPermitted, "Session parken, Arbeit bleibt erhalten"},
	"ReopenSession":           {ActionPermitted, "Session wieder öffnen"},
	"ResumeSession":           {ActionPermitted, "Session fortsetzen"},
	"FreshStartSession":       {ActionPermitted, "Session frisch starten"},
	"DiscardSession":          {ActionPermitted, "Eintrag ohne Runtime verwerfen"},
	"SetMainBranch":           {ActionPermitted, "Project-Einstellung"},
	"AddDivider":              {ActionPermitted, "Darstellung"},
	"RenameDivider":           {ActionPermitted, "Darstellung"},
	"RemoveDivider":           {ActionPermitted, "Darstellung"},
	"SetDividerCollapsed":     {ActionPermitted, "Darstellung"},
	"MoveSidebarItem":         {ActionPermitted, "Darstellung"},
	"AddReviewComment":        {ActionPermitted, "Review-Kommentar an der Session"},
	"EditReviewComment":       {ActionPermitted, "Review-Kommentar an der Session"},
	"DeleteReviewComment":     {ActionPermitted, "Review-Kommentar an der Session"},
	"DiscardSentReview":       {ActionPermitted, "Review-Kommentar an der Session"},
	"SendReview":              {ActionPermitted, "Review-Zustellung an die Session"},

	// Beschränkt: destruktiv oder hostförmig, Default ohne Opt-in zu.
	"RemoveWorktree": {ActionRestricted, "Entfernt einen Worktree auf dem Host — braucht Host-Opt-in"},
	"RemoveProject":  {ActionRestricted, "Entfernt ein Project auf dem Host — braucht Host-Opt-in"},
	"AddProject":     {ActionRestricted, "Registriert ein Project per Dateisystempfad — braucht Host-Opt-in"},
	"KillSession":    {ActionRestricted, "Beendet eine Session unwiderruflich — braucht Host-Opt-in"},

	// Rein lokale Bedienung: der Client führt sie selbst aus, der Host
	// verweigert sie über das Netz.
	"PickFolder":              {ActionRestricted, "Nur lokale Bedienung: Ordnerdialog dieses Rechners"},
	"SaveImage":               {ActionRestricted, "Nur lokale Bedienung: Zwischenablage dieses Rechners"},
	"BuildInfo":               {ActionRestricted, "Nur lokale Bedienung: Build-Info dieses Rechners"},
	"NotificationsEnabled":    {ActionRestricted, "Nur lokale Bedienung: Mitteilungen dieses Rechners"},
	"SetNotificationsEnabled": {ActionRestricted, "Nur lokale Bedienung: Mitteilungen dieses Rechners"},
	"BreakHeartbeat":          {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"TakeBreak":               {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"EndBreak":                {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"SnoozeBreak":             {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"BreakOver":               {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"SetBreakConfig":          {ActionRestricted, "Nur lokale Bedienung: Pause dieses Rechners"},
	"AzAccounts":              {ActionRestricted, "Nur lokale Bedienung: Cloud-Konten dieses Rechners"},
	"AzSetSubscription":       {ActionRestricted, "Nur lokale Bedienung: Cloud-Konten dieses Rechners"},
	"AzLogin":                 {ActionRestricted, "Nur lokale Bedienung: Cloud-Konten dieses Rechners"},
	"ArgoLogin":               {ActionRestricted, "Nur lokale Bedienung: Cloud-Konten dieses Rechners"},
	"ShowNotchEvent":          {ActionRestricted, "Nur lokale Bedienung: Notch dieses Rechners"},
	"ClearNotch":              {ActionRestricted, "Nur lokale Bedienung: Notch dieses Rechners"},
	"RespondToNotch":          {ActionRestricted, "Nur lokale Bedienung: Notch dieses Rechners"},
	"SetActiveTerm":           {ActionRestricted, "Nur lokale Bedienung: aktives Terminal dieses Rechners"},
	"WatchConversation":       {ActionRestricted, "Nur lokale Bedienung: Verlaufsleser dieses Rechners"},
	"Zeitgeist":               {ActionRestricted, "Nur lokale Bedienung: Zeitgeist dieses Rechners"},
	"ZeitgeistStart":          {ActionRestricted, "Nur lokale Bedienung: Zeitgeist dieses Rechners"},
	"ZeitgeistPause":          {ActionRestricted, "Nur lokale Bedienung: Zeitgeist dieses Rechners"},
	"ZeitgeistResume":         {ActionRestricted, "Nur lokale Bedienung: Zeitgeist dieses Rechners"},
	"ZeitgeistStop":           {ActionRestricted, "Nur lokale Bedienung: Zeitgeist dieses Rechners"},

	// Dateiliste des Hosts: kein Remote-Dateisystem-Stöbern (Non-Goal).
	"CompleteFiles": {ActionRestricted, "Dateiliste des Hosts — kein Remote-Dateisystem-Zugriff"},
}

// Classify ordnet eine Host-API-Methode ihrer Policy-Klasse zu. Der zweite
// Rückgabewert ist false für unbekannte Methoden — die verweigert der Host
// fail-closed, statt sie zu raten.
func Classify(method string) (PolicyEntry, bool) {
	entry, known := RemoteActionPolicy[method]
	return entry, known
}

// RestrictedError meldet eine hostseitig verweigerte Aktion. Sie ist bewusst
// kein Fehler im Sinne von „etwas ging schief", sondern eine Auskunft über
// die Policy — der Client zeigt sie als „nicht verfügbar" statt als
// Fehlschlag.
type RestrictedError struct {
	Method string
	Reason string
}

func (e *RestrictedError) Error() string {
	return fmt.Sprintf("%s ist für Remote-Clients beschränkt: %s", e.Method, e.Reason)
}

// EnforceRemote ist der hostseitige Durchsetzungspunkt aus D8. Er läuft vor
// jeder Nebenwirkung: erlaubt → nil, beschränkt oder unbekannt → Fehler.
func EnforceRemote(method string, optIn map[string]bool) error {
	entry, known := Classify(method)
	if !known {
		return &RestrictedError{Method: method, Reason: "unbekannte Methode — fail-closed verweigert"}
	}
	if entry.Class == ActionPermitted {
		return nil
	}
	if optIn[method] {
		return nil
	}
	return &RestrictedError{Method: method, Reason: entry.Reason}
}

// PolicyDocument ist, was der Host unter /v1/policy ausliefert und was der
// Client zum Ausgrauen liest. Die Reihenfolge ist stabil, damit Tests und
// Doku gegen dieselbe Gestalt prüfen.
func PolicyDocument() []PolicyMethodDoc {
	names := make([]string, 0, len(RemoteActionPolicy))
	for name := range RemoteActionPolicy {
		names = append(names, name)
	}
	sort.Strings(names)
	document := make([]PolicyMethodDoc, 0, len(names))
	for _, name := range names {
		entry := RemoteActionPolicy[name]
		document = append(document, PolicyMethodDoc{
			Method: name, Class: entry.Class, Reason: entry.Reason,
		})
	}
	return document
}

// PolicyMethodDoc ist eine Zeile des Policy-Dokuments.
type PolicyMethodDoc struct {
	Method string      `json:"method"`
	Class  ActionClass `json:"class"`
	Reason string      `json:"reason"`
}
