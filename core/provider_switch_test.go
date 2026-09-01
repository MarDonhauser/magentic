package core

import (
	"context"
	"testing"
)

func newRecordingLifecycle(t *testing.T) (*SessionLifecycle, *fakeLifecycleRuntime, *Registry, Project) {
	t.Helper()
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	return lifecycle, runtime, registry, project
}

func provisionSessionFor(t *testing.T, lifecycle *SessionLifecycle, project Project, vendor AgentVendor) Session {
	t.Helper()
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "navi", Directory: project.Path,
		Kind: SessionKindCodingAgent, Vendor: vendor,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return result.Session
}

func TestSwitchVendorRestartsAndKeepsRuns(t *testing.T) {
	lifecycle, runtime, _, project := newRecordingLifecycle(t)
	session := provisionSessionFor(t, lifecycle, project, AgentVendorClaude)
	runtime.reset()

	switched, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorCodex)
	if err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if switched.Vendor != AgentVendorCodex {
		t.Fatalf("Vendor = %q, want %q", switched.Vendor, AgentVendorCodex)
	}
	if _, ok := switched.AgentRun(AgentVendorClaude); !ok {
		t.Fatal("die Claude-Run-Ref muss erhalten bleiben")
	}
	if runtime.stopCalls != 1 || runtime.startCalls != 1 {
		t.Fatalf("Stop/Start = %d/%d, want 1/1", runtime.stopCalls, runtime.startCalls)
	}
	if runtime.lastStartMode != "new" {
		t.Fatalf("StartMode = %q, want \"new\"", runtime.lastStartMode)
	}
}

func TestSwitchVendorToKnownRunResumes(t *testing.T) {
	lifecycle, runtime, registry, project := newRecordingLifecycle(t)
	session := provisionSessionFor(t, lifecycle, project, AgentVendorClaude)
	if _, err := registry.Change(context.Background(), RecordAgentRun(
		session.ID, session.Name, AgentRunRef{Vendor: AgentVendorCodex, ExternalID: "run-9"},
	)); err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorCodex); err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if runtime.lastStartMode != "resume" {
		t.Fatalf("StartMode = %q, want \"resume\"", runtime.lastStartMode)
	}
}

func TestSwitchVendorToSameVendorIsNoop(t *testing.T) {
	lifecycle, runtime, _, project := newRecordingLifecycle(t)
	session := provisionSessionFor(t, lifecycle, project, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorClaude); err != nil {
		t.Fatalf("SwitchVendor: %v", err)
	}
	if runtime.stopCalls != 0 || runtime.startCalls != 0 {
		t.Fatalf("Stop/Start = %d/%d, want 0/0", runtime.stopCalls, runtime.startCalls)
	}
}

func TestSwitchVendorRejectsTerminal(t *testing.T) {
	lifecycle, _, _, project := newRecordingLifecycle(t)
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "term-navi", Directory: project.Path, Kind: SessionKindTerminal,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, err := lifecycle.SwitchVendor(context.Background(), result.Session.ID, AgentVendorCodex); err == nil {
		t.Fatal("eine Terminal-Session hat keinen Vendor")
	}
}

func TestSwitchVendorRejectsUnknownVendor(t *testing.T) {
	lifecycle, runtime, _, project := newRecordingLifecycle(t)
	session := provisionSessionFor(t, lifecycle, project, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendor("cursor")); err == nil {
		t.Fatal("unbekannter Vendor muss abgelehnt werden")
	}
	if runtime.stopCalls != 0 {
		t.Fatal("bei einem abgelehnten Wechsel darf nichts beendet werden")
	}
}

func TestSwitchVendorRequiresBinary(t *testing.T) {
	provider, ok := providerForVendor(AgentVendorGemini)
	if !ok {
		t.Fatal("kein Gemini-Provider")
	}
	if providerBinaryAvailable(provider) {
		t.Skip("gemini ist auf dieser Maschine installiert")
	}
	lifecycle, runtime, _, project := newRecordingLifecycle(t)
	session := provisionSessionFor(t, lifecycle, project, AgentVendorClaude)
	runtime.reset()
	if _, err := lifecycle.SwitchVendor(context.Background(), session.ID, AgentVendorGemini); err == nil {
		t.Fatal("ein Vendor ohne Binary darf nicht übernommen werden")
	}
	if runtime.stopCalls != 0 {
		t.Fatal("die laufende Session muss unberührt bleiben")
	}
}
