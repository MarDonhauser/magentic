package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHistoryUsageRejectsMalformedOrAmbiguousNumbers(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "negative", raw: `{"input_tokens":-1}`},
		{name: "fractional", raw: `{"input_tokens":1.9}`},
		{name: "overflow", raw: `{"input_tokens":9223372036854775808}`},
		{name: "conflicting aliases", raw: `{"input_tokens":1,"nested":{"prompt_tokens":2}}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			usage, invalid := historyUsageFromRaw(json.RawMessage(test.raw))
			if !invalid || usage.InputKnown || usage.Input != 0 {
				t.Fatalf("historyUsageFromRaw(%s) = %+v, invalid=%v", test.raw, usage, invalid)
			}
		})
	}
}

func TestHistoryUsageAcceptsOneConsistentNonnegativeIntegerFact(t *testing.T) {
	usage, invalid := historyUsageFromRaw(json.RawMessage(`{
		"input_tokens":12,
		"nested":{"prompt_tokens":12},
		"output_tokens":0
	}`))
	if invalid || !usage.InputKnown || usage.Input != 12 || !usage.OutputKnown || usage.Output != 0 {
		t.Fatalf("valid usage = %+v, invalid=%v", usage, invalid)
	}
}

func TestMalformedUsageMakesProviderParsePartial(t *testing.T) {
	line := []byte(`{"type":"assistant","timestamp":"2026-08-20T10:00:00Z","sessionId":"conversation","message":{"role":"assistant","content":"answer","usage":{"input_tokens":1.5}}}` + "\n")
	records, problems, err := (claudeHistoryAdapter{root: "/history"}).Parse(
		context.Background(), nil, "/history/project/conversation.jsonl", line,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Usage.InputKnown {
		t.Fatalf("malformed Usage crossed as known: %#v", records)
	}
	if len(problems) == 0 {
		t.Fatal("malformed Usage did not produce a provider coverage problem")
	}
}
