package core

import "testing"

// ParseManagedEventLine reads exactly the shapes verified against CLI 2.1.259
// and reports false for everything else. Anything unrecognized must be
// ignored rather than guessed at: a protocol break degrades to "managed
// runtime unavailable", never to a Session that silently does nothing.
func TestManagedProtocolParsesVerifiedShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want ManagedEvent
		ok   bool
	}{
		{
			name: "Echo mit Text-Inhalt",
			line: `{"type":"user","message":{"role":"user","content":"hallo"}}`,
			want: ManagedEvent{Kind: ManagedEventEcho, EchoText: "hallo"},
			ok:   true,
		},
		{
			name: "Echo mit Content-Blöcken",
			line: `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hallo "},{"type":"text","text":"welt"}]}}`,
			want: ManagedEvent{Kind: ManagedEventEcho, EchoText: "hallo welt"},
			ok:   true,
		},
		{
			name: "Echo ohne Text ist keine Zustellbestätigung",
			line: `{"type":"user","message":{"role":"user","content":[]}}`,
		},
		{
			name: "Echo einer anderen Rolle",
			line: `{"type":"user","message":{"role":"assistant","content":"hallo"}}`,
		},
		{
			name: "Streamender Delta-Chunk",
			line: `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}}`,
			want: ManagedEvent{Kind: ManagedEventChunk, ChunkText: "Hi"},
			ok:   true,
		},
		{
			name: "Stream-Event ohne Delta",
			line: `{"type":"stream_event","event":{"type":"content_block_start","text":"Hi"}}`,
			want: ManagedEvent{Kind: ManagedEventChunk, ChunkText: "Hi"},
			ok:   true,
		},
		{
			name: "Leerer Chunk trägt keine Ausgabe",
			line: `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}}`,
		},
		{
			name: "Fertige Assistant-Nachricht",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fertig"}]}}`,
			want: ManagedEvent{Kind: ManagedEventChunk, ChunkText: "fertig"},
			ok:   true,
		},
		{
			name: "Erfolgreiches Turn-Ende",
			line: `{"type":"result","subtype":"success","result":"ok"}`,
			want: ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndCompleted},
			ok:   true,
		},
		{
			name: "Turn-Ende mit Vendor-Subtype als Grund",
			line: `{"type":"result","subtype":"error_max_turns"}`,
			want: ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndFailed, FailReason: "error_max_turns"},
			ok:   true,
		},
		{
			name: "Turn-Ende mit eigener Fehlermeldung",
			line: `{"type":"result","subtype":"error_during_execution","is_error":true,"error":"upstream timeout"}`,
			want: ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndFailed, FailReason: "upstream timeout"},
			ok:   true,
		},
		{
			name: "Turn-Ende mit Fehlerliste",
			line: `{"type":"result","subtype":"","is_error":true,"errors":["a","b"]}`,
			want: ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndFailed, FailReason: "a; b"},
			ok:   true,
		},
		{
			name: "Fehler ohne Grund wird trotzdem benannt",
			line: `{"type":"result","subtype":"","is_error":true}`,
			want: ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndFailed, FailReason: "der Agent meldete einen Fehler ohne Grund"},
			ok:   true,
		},
		{
			name: "System-Init trägt keine Turn-Tatsache",
			line: `{"type":"system","subtype":"init","session_id":"run-1"}`,
		},
		{
			name: "Unbekannter Typ einer neueren CLI",
			line: `{"type":"something_new","payload":{}}`,
		},
		{
			name: "Zeile ohne Typ",
			line: `{"message":{"role":"user","content":"hallo"}}`,
		},
		{
			name: "Kaputtes JSON",
			line: `{"type":"user"`,
		},
		{
			name: "Leere Zeile",
			line: ``,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseManagedEventLine([]byte(tc.line))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (event %+v)", ok, tc.ok, got)
			}
			if !tc.ok {
				if got != (ManagedEvent{}) {
					t.Fatalf("an ignored line must yield no event, got %+v", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("event = %+v, want %+v", got, tc.want)
			}
		})
	}
}
