package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRegistrySetSessionServiceTogglesAndProjectsToOverview(t *testing.T) {
	registry := OpenRegistry(filepath.Join(t.TempDir(), "state.json"))
	session := Session{ID: "session-svc", Name: "devserver", Dir: "/workspace/project"}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}

	result, err := registry.Change(context.Background(), SetSessionService(session.ID, session.Name, true))
	if err != nil || !result.Applied {
		t.Fatalf("SetSessionService(true) = %+v, %v", result, err)
	}
	state := result.Snapshot.State()
	saved := state.SessionByID(session.ID)
	if saved == nil || !saved.Service {
		t.Fatalf("service flag was not persisted: %+v", saved)
	}
	if got := toOvAgent(*saved, SessionObservation{}, ""); !got.Service {
		t.Fatalf("overview projection lost the service flag: %+v", got)
	}

	result, err = registry.Change(context.Background(), SetSessionService(session.ID, session.Name, true))
	if err != nil || result.Applied {
		t.Fatalf("repeated SetSessionService(true) must be a no-op, got %+v, %v", result, err)
	}

	result, err = registry.Change(context.Background(), SetSessionService(session.ID, session.Name, false))
	if err != nil || !result.Applied {
		t.Fatalf("SetSessionService(false) = %+v, %v", result, err)
	}
	state = result.Snapshot.State()
	if saved := state.SessionByID(session.ID); saved == nil || saved.Service {
		t.Fatalf("service flag was not cleared: %+v", saved)
	}

	if _, err := registry.Change(context.Background(), SetSessionService("missing", "missing", true)); err == nil {
		t.Fatal("SetSessionService on an unknown session must fail")
	}
}
