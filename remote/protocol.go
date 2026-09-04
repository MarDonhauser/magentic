package remote

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion ist die einzige Version, die Host und Client sprechen (D2,
// Non-Goal: kein Versions-Schema jenseits des Handshakes). Beide werden von
// derselben Person gemeinsam aktualisiert; eine Abweichung wird verweigert.
const ProtocolVersion = 1

// ErrorCode unterscheidet am Draht, was schiefging — damit der Client eine
// abgewiesene Anmeldedaten nicht als Transportproblem zeigt und eine
// beschränkte Aktion nicht als Fehlschlag.
type ErrorCode string

const (
	// ErrorAuth meldet fehlende, unbekannte oder widerrufene HostTokens.
	ErrorAuth ErrorCode = "auth"
	// ErrorRestricted meldet eine vom Host verweigerte beschränkte Aktion.
	ErrorRestricted ErrorCode = "restricted"
	// ErrorObservation meldet, dass der Host selbst nicht beobachten konnte
	// (tmux-Sonde gescheitert) — zu unterscheiden von „Client erreicht den
	// Host nicht", was gar keine Antwort liefert.
	ErrorObservation ErrorCode = "observation"
	// ErrorTransport meldet Drahtfehler unterhalb der Methode.
	ErrorTransport ErrorCode = "transport"
	// ErrorVersion meldet einen Versions-Handshake, der nicht passt.
	ErrorVersion ErrorCode = "version"
	// ErrorMethod meldet Unbekanntes und Nicht-Implementiertes fail-closed.
	ErrorMethod ErrorCode = "method"
	// ErrorInternal meldet einen hostseitigen Fehlschlag unterhalb der
	// Methode (Registry, tmux, Git) — weder Auth noch Policy noch Draht.
	ErrorInternal ErrorCode = "error"
)

// WireError ist die Fehlergestalt jeder Antwort. Details tragen niemals einen
// Token-Wert (vgl. auth.go).
type WireError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *WireError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Request ist ein unärer Aufruf: Methode aus HostAPIMethods plus JSON-
// Parameter in der Form, die Wails heute schon an das Frontend reicht.
type Request struct {
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	Identity string          `json:"identity,omitempty"`
}

// Response antwortet auf genau einen Request mit derselben ID. Genau eines
// von Result und Error ist gesetzt.
type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *WireError      `json:"error,omitempty"`
}

// Hello eröffnet jede Verbindung (unär wie Stream) mit dem Handshake.
type Hello struct {
	Version int    `json:"version"`
	Client  string `json:"client,omitempty"`
}

// Welcome bestätigt den Handshake oder verweigert ihn per WireError mit
// ErrorVersion.
type Welcome struct {
	Version int    `json:"version"`
	Host    string `json:"host,omitempty"`
}

// CheckHandshake verweigert alles außer der einen gemeinsamen Version.
func CheckHandshake(hello Hello) (Welcome, *WireError) {
	if hello.Version != ProtocolVersion {
		return Welcome{}, &WireError{
			Code:    ErrorVersion,
			Message: fmt.Sprintf("Protokollversion %d wird nicht bedient (Host spricht %d)", hello.Version, ProtocolVersion),
		}
	}
	return Welcome{Version: ProtocolVersion}, nil
}

// EncodeParams verpackt Aufrufparameter; DecodeParams holt sie typisiert
// wieder heraus. Identity trägt die client-generierte Transition-Identität
// aus D7 (Aktions-Wiederholung wird idempotent statt doppelt).
func EncodeParams(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

// ErrorResult baut eine Antwort mit Fehler zur Request-ID.
func ErrorResult(request Request, code ErrorCode, message string) Response {
	return Response{Version: ProtocolVersion, ID: request.ID, Error: &WireError{Code: code, Message: message}}
}
