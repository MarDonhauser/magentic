package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// trunc and pad take an ASCII fast path. It has to agree with the measured
// display width on every input the TUI can hand it: plain text, umlauts, wide
// glyphs and already-styled strings carrying escape sequences.
func TestTruncKeepsMeasuredWidth(t *testing.T) {
	cases := []struct {
		name  string
		input string
		width int
	}{
		{"ascii kurz", "abc", 10},
		{"ascii exakt", "abcdefghij", 10},
		{"ascii zu lang", "abcdefghijklmno", 10},
		{"umlaute", "Grüße für Änderungen", 10},
		{"breite glyphen", "日本語のテキスト", 10},
		{"emoji", "status ✅ fertig ⑂", 10},
		{"gestylt", styleDim.Render("abcdefghijklmno"), 10},
		{"breite eins", "abcdef", 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := trunc(testCase.input, testCase.width)
			if width := lipgloss.Width(got); width > testCase.width {
				t.Fatalf("trunc(%q, %d) = %q mit Breite %d", testCase.input, testCase.width, got, width)
			}
			if !strings.HasPrefix(testCase.input, got) && !strings.Contains(got, "…") {
				t.Fatalf("trunc(%q, %d) = %q ist weder Präfix noch gekürzt", testCase.input, testCase.width, got)
			}
		})
	}
}

func TestPadReachesExactWidth(t *testing.T) {
	for _, input := range []string{"", "abc", "Grüße", "日本語", styleDim.Render("abc")} {
		got := pad(input, 12)
		if width := lipgloss.Width(got); width != 12 {
			t.Fatalf("pad(%q, 12) = %q mit Breite %d", input, got, width)
		}
	}
	if got := pad("abcdefghijklmno", 5); lipgloss.Width(got) != 5 {
		t.Fatalf("pad kürzt zu lange Eingabe nicht auf 5: %q", got)
	}
}

// The painter replaces Style.Render on the preview's hot path and must produce
// exactly the same escape sequences for a single line.
func TestLinePainterMatchesStyleRender(t *testing.T) {
	for _, line := range []string{"", "abc", "Grüße für alle"} {
		if got, want := paintDim.paint(line), styleDim.Render(line); got != want {
			t.Fatalf("paint(%q) = %q, Style.Render = %q", line, got, want)
		}
	}
}
