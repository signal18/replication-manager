package server

import (
	"path/filepath"

	"github.com/signal18/replication-manager/utils/tty"
)

func (repman *ReplicationManager) InitWebTTY() {
	// Initialize the session manager
	var terminalManager tty.TerminalManager
	stateFile := filepath.Join(repman.Conf.WorkingDir, "tty.state.json")

	if repman.Conf.TerminalSessionManager == "tmux" {
		terminalManager = &tty.TmuxManager{}
	} else if repman.Conf.TerminalSessionManager == "screen" {
		terminalManager = &tty.ScreenManager{}
	}

	repman.SessionManager = tty.NewSessionManager(stateFile, repman.Conf.TerminalSessionResume, terminalManager, repman.Logrus)
}
