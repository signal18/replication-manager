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
			name:    "empty rid defaults to db container",
			rid:     "",
			want:    defaultServerServiceContainer,
			wantErr: false,
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

func TestResolveTerminalContainerRIDForSession(t *testing.T) {
	tests := []struct {
		name           string
		isNodeTerminal bool
		cmdType        tty.TerminalCommandType
		orchestrator   string
		rid            string
		wantRID        string
		wantShouldSet  bool
		wantErr        bool
	}{
		{
			name:           "opensvc server bash defaults to db",
			isNodeTerminal: true,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOpenSVC,
			rid:            "",
			wantRID:        defaultServerServiceContainer,
			wantShouldSet:  true,
			wantErr:        false,
		},
		{
			name:           "opensvc server bash jobs override accepted",
			isNodeTerminal: true,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOpenSVC,
			rid:            cluster.RestartRidJobsContainer,
			wantRID:        cluster.RestartRidJobsContainer,
			wantShouldSet:  true,
			wantErr:        false,
		},
		{
			name:           "rid rejected for proxy bash terminal",
			isNodeTerminal: false,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOpenSVC,
			rid:            cluster.RestartRidJobsContainer,
			wantRID:        "",
			wantShouldSet:  false,
			wantErr:        true,
		},
		{
			name:           "rid rejected for opensvc mysql terminal",
			isNodeTerminal: true,
			cmdType:        tty.TerminalMySQL,
			orchestrator:   config.ConstOrchestratorOpenSVC,
			rid:            cluster.RestartRidJobsContainer,
			wantRID:        "",
			wantShouldSet:  false,
			wantErr:        true,
		},
		{
			name:           "rid rejected for non-opensvc server bash",
			isNodeTerminal: true,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOnPremise,
			rid:            cluster.RestartRidJobsContainer,
			wantRID:        "",
			wantShouldSet:  false,
			wantErr:        true,
		},
		{
			name:           "empty rid ignored for non-opensvc path",
			isNodeTerminal: true,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOnPremise,
			rid:            "",
			wantRID:        "",
			wantShouldSet:  false,
			wantErr:        false,
		},
		{
			name:           "invalid opensvc rid rejected",
			isNodeTerminal: true,
			cmdType:        tty.TerminalBash,
			orchestrator:   config.ConstOrchestratorOpenSVC,
			rid:            "container#prx",
			wantRID:        "",
			wantShouldSet:  false,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRID, gotShouldSet, err := resolveTerminalContainerRIDForSession(tt.isNodeTerminal, tt.cmdType, tt.orchestrator, tt.rid)
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
