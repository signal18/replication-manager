package state

import "testing"

func TestBuildStateKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		serverUrl string
		want      string
	}{
		{"no server url returns bare key", "APPERR002", "", "APPERR002"},
		{"server url appends scoped suffix", "APPERR002", "app.example.com", "APPERR002@app.example.com"},
		{"already-scoped key is left unchanged", "APPERR002@app.example.com", "app.example.com", "APPERR002@app.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildStateKey(tt.key, tt.serverUrl); got != tt.want {
				t.Fatalf("BuildStateKey(%q, %q) = %q, want %q", tt.key, tt.serverUrl, got, tt.want)
			}
		})
	}
}

func TestAddState_ScopesKeyByServerUrl(t *testing.T) {
	sm := &StateMachine{}
	sm.Init()

	sm.AddState("APPERR002", State{ErrType: "WARN", ServerUrl: "app-a.example.com", ErrDesc: "a"})
	sm.AddState("APPERR002", State{ErrType: "WARN", ServerUrl: "app-b.example.com", ErrDesc: "b"})

	if !sm.IsInState("APPERR002@app-a.example.com") {
		t.Fatal("expected app A's scoped state to be present")
	}
	if !sm.IsInState("APPERR002@app-b.example.com") {
		t.Fatal("expected app B's scoped state to be present")
	}
}

// A server that misses a ping keeps its states for the tick: they must resolve
// under their bare ErrKey, the key StateProcessing matches to release the backup
// slot (WARN0175, WARN0073) or start a reseed (WARN0074).
func TestCopyOldStateFromUnknowServer_KeepsErrKey(t *testing.T) {
	sm := &StateMachine{}
	sm.Init()
	const srv = "db2:3306"

	sm.AddState("WARN0175", State{ErrType: "WARNING", ServerUrl: srv})
	sm.AddState("WARN0048", State{ErrType: "WARNING", ServerUrl: "db1:3306"})
	sm.ClearState() // tick: the ping of db2 fails, its states are carried over
	sm.CopyOldStateFromUnknowServer(srv)
	sm.ClearState() // tick: db2 answers, its backup has ended

	var resolved []State
	for _, s := range sm.GetResolvedStates() {
		if s.ServerUrl == srv {
			resolved = append(resolved, s)
		}
	}
	if len(resolved) != 1 || resolved[0].ErrKey != "WARN0175" {
		t.Fatalf("resolved states of %s = %+v, want one with ErrKey WARN0175", srv, resolved)
	}
}
