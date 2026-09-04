package remote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// FrameKind nennt, was ein Stream-Rahmen trägt.
type FrameKind string

const (
	// FrameTermOutput trägt Terminal-Bytes (base64, wie die heutigen
	// term:data:-Events) mit monotoner Sequenznummer ab Stream-Ursprung.
	FrameTermOutput FrameKind = "term-output"
	// FrameStatusEvent trägt eine Observation-Änderung, damit Clients nicht
	// pollen müssen.
	FrameStatusEvent FrameKind = "status-event"
	// FrameGap markiert ehrlich, dass Bytes fehlen: Der Client ersetzt
	// seinen Inhalt durch den mitgelieferten Snapshot, statt über die Lücke
	// zu appenden (D5).
	FrameGap FrameKind = "gap"
	// FrameControl trägt Steuerung (ping, resume-quittung, Stream-Ende).
	FrameControl FrameKind = "control"
)

// Frame ist ein Rahmen des Streaming-Kanals. Payload ist base64-codierter
// Inhalt (Terminal-Bytes bzw. Snapshot-Text), Event ist eine Statusmeldung,
// Control ein Steuerwort. Genau eines ist je nach Kind gesetzt.
type Frame struct {
	Kind    FrameKind   `json:"kind"`
	Seq     uint64      `json:"seq"`
	Payload string      `json:"payload,omitempty"`
	Event   StatusEvent `json:"event,omitempty"`
	Control string      `json:"control,omitempty"`
}

// StatusEvent beschreibt eine Observation-Änderung einer Session.
type StatusEvent struct {
	SessionID string `json:"sessionId"`
	Presence  string `json:"presence,omitempty"`
	Activity  string `json:"activity,omitempty"`
	Attention string `json:"attention,omitempty"`
}

// TermFrame baut einen Terminal-Output-Rahmen aus rohen Bytes.
func TermFrame(seq uint64, data []byte) Frame {
	return Frame{
		Kind:    FrameTermOutput,
		Seq:     seq,
		Payload: base64.StdEncoding.EncodeToString(data),
	}
}

// TermBytes holt die rohen Bytes aus einem Terminal-Rahmen zurück.
func (f Frame) TermBytes() ([]byte, error) {
	if f.Kind != FrameTermOutput && f.Kind != FrameGap {
		return nil, fmt.Errorf("Rahmen %q trägt keine Terminal-Bytes", f.Kind)
	}
	if f.Payload == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(f.Payload)
}

// GapFrame baut die ehrliche Lücke: neuer Ursprung ab NextSeq plus frischer
// Pane-Snapshot als Ersatzinhalt.
func GapFrame(nextSeq uint64, snapshot []byte) Frame {
	return Frame{
		Kind:    FrameGap,
		Seq:     nextSeq,
		Payload: base64.StdEncoding.EncodeToString(snapshot),
	}
}

// MarshalFrame / UnmarshalFrame sind die runde Wahrheit des Kanals.
func MarshalFrame(frame Frame) ([]byte, error) {
	return json.Marshal(frame)
}

// UnmarshalFrame liest einen Rahmen und verweigert unbekannte Arten
// fail-closed, statt sie zu raten.
func UnmarshalFrame(data []byte) (Frame, error) {
	var frame Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		return Frame{}, err
	}
	switch frame.Kind {
	case FrameTermOutput, FrameStatusEvent, FrameGap, FrameControl:
		return frame, nil
	default:
		return Frame{}, fmt.Errorf("unbekannte Rahmenart %q", frame.Kind)
	}
}
