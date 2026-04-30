package server

import (
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/tty"
)

func TestResolveOpenSVCTerminalContainerRID(t *testing.T) {
	tests := []struct {
		name    string
		rid     string
		want    string
		wantErr bool
	}{
		{
			name:    "empty rid rejected by value validator",
			rid:     "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "explicit db container accepted",
			rid:     defaultServerServiceContainer,
			want:    defaultServerServiceContainer,
			wantErr: false,
		},
		{
			name:    "explicit jobs container accepted",
			rid:     cluster.RestartRidJobsContainer,
			want:    cluster.RestartRidJobsContainer,
			wantErr: false,
		},
		{
			name:    "invalid rid rejected",
			rid:     "container#prx",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOpenSVCTerminalContainerRID(tt.rid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveOpenSVCTerminalContainerRID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("resolveOpenSVCTerminalContainerRID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveOpenSVCAppTerminalContainerRID(t *testing.T) {
	tests := []struct {
		name    string
		rid     string
		want    string
		wantErr bool
	}{
		{
			name:    "explicit app container accepted",
			rid:     defaultAppServiceContainer,
			want:    defaultAppServiceContainer,
			wantErr: false,
		},
		{
			name:    "invalid rid rejected",
			rid:     defaultServerServiceContainer,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOpenSVCAppTerminalContainerRID(tt.rid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveOpenSVCAppTerminalContainerRID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("resolveOpenSVCAppTerminalContainerRID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTerminalContainerRIDForSession(t *testing.T) {
	tests := []struct {
		name          string
		targetKind    string
		cmdType       tty.TerminalCommandType
		orchestrator  string
		rid           string
		wantRID       string
		wantShouldSet bool
		wantErr       bool
	}{
		{
			name:          "opensvc server bash defaults to db",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           "",
			wantRID:       defaultServerServiceContainer,
			wantShouldSet: true,
			wantErr:       false,
		},
		{
			name:          "opensvc app bash defaults to app",
			targetKind:    terminalTargetApp,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           "",
			wantRID:       defaultAppServiceContainer,
			wantShouldSet: true,
			wantErr:       false,
		},
		{
			name:          "opensvc server bash jobs override accepted",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           cluster.RestartRidJobsContainer,
			wantRID:       cluster.RestartRidJobsContainer,
			wantShouldSet: true,
			wantErr:       false,
		},
		{
			name:          "opensvc app bash app rid accepted",
			targetKind:    terminalTargetApp,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           defaultAppServiceContainer,
			wantRID:       defaultAppServiceContainer,
			wantShouldSet: true,
			wantErr:       false,
		},
		{
			name:          "rid rejected for proxy bash terminal",
			targetKind:    terminalTargetProxy,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           cluster.RestartRidJobsContainer,
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       true,
		},
		{
			name:          "rid rejected for opensvc mysql terminal",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalMySQL,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           cluster.RestartRidJobsContainer,
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       true,
		},
		{
			name:          "rid rejected for non-opensvc server bash",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOnPremise,
			rid:           cluster.RestartRidJobsContainer,
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       true,
		},
		{
			name:          "empty rid ignored for non-opensvc path",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOnPremise,
			rid:           "",
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       false,
		},
		{
			name:          "invalid opensvc rid rejected",
			targetKind:    terminalTargetServer,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           "container#prx",
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       true,
		},
		{
			name:          "invalid app rid rejected",
			targetKind:    terminalTargetApp,
			cmdType:       tty.TerminalBash,
			orchestrator:  config.ConstOrchestratorOpenSVC,
			rid:           defaultServerServiceContainer,
			wantRID:       "",
			wantShouldSet: false,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRID, gotShouldSet, err := resolveTerminalContainerRIDForSession(tt.targetKind, tt.cmdType, tt.orchestrator, tt.rid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveTerminalContainerRIDForSession() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotRID != tt.wantRID {
				t.Fatalf("resolveTerminalContainerRIDForSession() rid = %q, want %q", gotRID, tt.wantRID)
			}
			if gotShouldSet != tt.wantShouldSet {
				t.Fatalf("resolveTerminalContainerRIDForSession() shouldSet = %v, want %v", gotShouldSet, tt.wantShouldSet)
			}
		})
	}
}
