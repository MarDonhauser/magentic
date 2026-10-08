package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ompRuntimeForTest returns an omp lifecycle runtime whose spawn starts an
// in-process host running a scripted omp, instead of a Magentic binary.
func ompRuntimeForTest(t *testing.T, script string, onSpawn func(Session)) (ompLifecycleRuntime, *[]*AgentHost) {
	t.Helper()
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "sh"
	t.Cleanup(func() { ompBinary = previous })
	previousGetState := ompSendInitialGetState
	ompSendInitialGetState = false
	t.Cleanup(func() { ompSendInitialGetState = previousGetState })
	registry := OpenManagedHostRegistry(filepath.Join(filepath.Dir(StatePath()), "managed-hosts.json"))
	var hosts []*AgentHost
	runtime := ompLifecycleRuntime{registry: registry, spawn: func(session Session, token AgentHostToken) (<-chan string, error) {
		if onSpawn != nil {
			onSpawn(session)
		}
		host, err := StartAgentHost(session.ID, token)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { host.Close() })
		hosts = append(hosts, host)
		record, _, _ := registry.RecordFor(session.ID)
		if _, err := host.StartOmpProcess(append([]string{"-c", script, "sh"}, record.LaunchArgv...), session.Dir); err != nil {
			host.Close()
			exited := make(chan string, 1)
			exited <- err.Error()
			return exited, nil
		}
		return make(chan string), nil
	}}
	return runtime, &hosts
}

func ompLifecycleSession(t *testing.T) Session {
	return Session{ID: "session-omp", Name: "orbit", RuntimeName: "mgt-orbit", Dir: t.TempDir(),
		Runtime: RuntimeOmp, Vendor: AgentVendorOmp, SessionKind: SessionKindCodingAgent, Model: "ollama/qwen3.5:9b"}
}

// 6.1 (replace-tmux-agent-runtime) and 2.12/2.13: the launch is recorded with
// the gate before the host exists, the host runs in the Session's directory,
// and Stop ends it and forgets the record.
func TestOmpLifecycleRecordsLaunchBeforeSpawnThenStartsAndStops(t *testing.T) {
	session := ompLifecycleSession(t)
	var recordedBeforeSpawn []string
	var runtime ompLifecycleRuntime
	runtime, hosts := ompRuntimeForTest(t, `echo '`+ompTestReady+`'; sleep 30`, func(s Session) {
		record, found, _ := runtime.registry.RecordFor(s.ID)
		if found && !record.Started {
			recordedBeforeSpawn = record.LaunchArgv
		}
	})
	if err := runtime.Start(context.Background(), session, "new"); err != nil {
		t.Fatal(err)
	}
	if !OmpLaunchArgvHasGate(recordedBeforeSpawn) {
		t.Fatalf("launch recorded before spawn = %v, want the gate", recordedBeforeSpawn)
	}
	cwd := ""
	for i, arg := range recordedBeforeSpawn {
		if arg == "--cwd" && i+1 < len(recordedBeforeSpawn) {
			cwd = recordedBeforeSpawn[i+1]
		}
	}
	if cwd != session.Dir {
		t.Fatalf("--cwd = %q, want the Session's own directory %q", cwd, session.Dir)
	}
	record, found, _ := runtime.registry.RecordFor(session.ID)
	if !found || !record.Started {
		t.Fatalf("record = %+v, want it marked started", record)
	}
	if exists, err := runtime.Exists(context.Background(), session); err != nil || !exists {
		t.Fatalf("exists = %v, %v, want the confirmed host", exists, err)
	}

	if err := runtime.Stop(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if (*hosts)[0].HostState().Alive {
		t.Fatal("Stop must end the omp process")
	}
	if _, found, _ := runtime.registry.RecordFor(session.ID); found {
		t.Fatal("a stopped host must be forgotten")
	}
}

// 2.4/2.8/6.13: a host that refuses — here omp's own "model not found" — fails
// the start with that reason and leaves no running host.
func TestOmpLifecycleStartReportsTheHostsRefusal(t *testing.T) {
	session := ompLifecycleSession(t)
	runtime, _ := ompRuntimeForTest(t, `echo 'Model "x/y" not found.'; exit 0`, nil)
	err := runtime.Start(context.Background(), session, "new")
	if err == nil || !strings.Contains(err.Error(), `Model "x/y" not found`) {
		t.Fatalf("err = %v, want the host's own refusal", err)
	}
	if exists, _ := runtime.Exists(context.Background(), session); exists {
		t.Fatal("a refused start must leave no confirmed host")
	}
}

// Start refuses a missing working directory before recording or starting
// anything.
func TestOmpLifecycleStartRefusesAMissingDirectory(t *testing.T) {
	session := ompLifecycleSession(t)
	session.Dir = filepath.Join(session.Dir, "missing")
	spawned := false
	runtime, _ := ompRuntimeForTest(t, `sleep 30`, func(Session) { spawned = true })
	if err := runtime.Start(context.Background(), session, "new"); err == nil || spawned {
		t.Fatalf("err = %v, spawned = %v: want a refusal before any spawn", err, spawned)
	}
	if _, found, _ := runtime.registry.RecordFor(session.ID); found {
		t.Fatal("nothing may be recorded for a start that cannot happen")
	}
}

// 3.8: the initial prompt is confirmed only by omp's echo.
func TestOmpLifecycleConfirmsTheInitialPromptOnItsEcho(t *testing.T) {
	session := ompLifecycleSession(t)
	runtime, _ := ompRuntimeForTest(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"type":"message_start","message":{"role":"user","content":"los"}}'
sleep 30`, nil)
	if err := runtime.Start(context.Background(), session, "new"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := runtime.DeliverInitial(context.Background(), session, "los")
	if err != nil || !confirmed {
		t.Fatalf("confirmed = %v, err = %v: want confirmation from the echo", confirmed, err)
	}
}

// Stop leaves a host alone whose token does not match the record.
func TestOmpLifecycleStopLeavesAForeignHostAlone(t *testing.T) {
	session := ompLifecycleSession(t)
	runtime, hosts := ompRuntimeForTest(t, `echo '`+ompTestReady+`'; sleep 30`, nil)
	if err := runtime.Start(context.Background(), session, "new"); err != nil {
		t.Fatal(err)
	}
	record, _, _ := runtime.registry.RecordFor(session.ID)
	if err := runtime.registry.RecordLaunchIntent(session.ID, record.SocketPath, NewAgentHostToken(), record.LaunchArgv); err != nil {
		t.Fatal(err)
	}
	err := runtime.Stop(context.Background(), session)
	if !errors.Is(err, ErrAgentHostForeign) {
		t.Fatalf("err = %v, want the foreign host refused", err)
	}
	if !(*hosts)[0].HostState().Alive {
		t.Fatal("a host that could not be confirmed must not be stopped")
	}
	_ = os.Remove(record.SocketPath + ".log")
}
