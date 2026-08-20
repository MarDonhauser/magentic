package core

import (
	"reflect"
	"testing"
)

func TestObservationSessionsAssignsCopyOnlyFixtureIDs(t *testing.T) {
	fixtures := []Session{{Name: "one"}, {Name: "two"}}
	before := append([]Session(nil), fixtures...)

	prepared := observationSessions(fixtures)
	if !reflect.DeepEqual(fixtures, before) {
		t.Fatalf("fixture Sessions were mutated: before=%#v after=%#v", before, fixtures)
	}
	if prepared[0].ID == "" || prepared[1].ID == "" || prepared[0].ID == prepared[1].ID {
		t.Fatalf("copy-only IDs are not usable: %#v", prepared)
	}
}
