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
