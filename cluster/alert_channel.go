// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"time"

	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/state"
)

// AlertChannel is one way of telling the operator: mail, Slack, Pushover, Teams, the alert
// script, Prometheus Alertmanager, Telegram. The cluster holds one value per channel in its
// registry (newAlertChannelList); every alert reaches the channels through
// cluster.dispatchAlert, which hands each accepting channel its own bounded queue, so a dead
// channel never stalls the monitor (F2, T18). Nothing sends an alert outside the registry:
// TestNoAlertSendOutsideRegistry (alert_channel_dispatch_test.go).
//
// Design: doc/implementation/cluster/ALERT_CHANNELS.md.
type AlertChannel interface {
	// Name is the channel's name: one of the ConstAlertChannel* constants.
	Name() string
	// Enabled is the channel's off-switch (T14): its settings make it send. Read at each
	// alert, so a setting change through the API applies at the next alert.
	Enabled() bool
	// Accepts tells whether the channel sends this event. It carries the rule each channel
	// had before the registry (which levels, which states), so the routing is unchanged.
	Accepts(ev AlertEvent) bool
	// Send delivers one event. It runs on the channel's own worker, never on the monitor
	// tick, and honours ctx, whose deadline is Timeout.
	Send(ctx context.Context, ev AlertEvent) error
	// Timeout bounds one Send.
	Timeout() time.Duration
}

// The channel names, as the drop and failure states name them.
const (
	ConstAlertChannelMail         = "mail"
	ConstAlertChannelSlack        = "slack"
	ConstAlertChannelPushover     = "pushover"
	ConstAlertChannelTeams        = "teams"
	ConstAlertChannelScript       = "script"
	ConstAlertChannelAlertmanager = "alertmanager"
	ConstAlertChannelTelegram     = "telegram"
)

// AlertEventKind is what happened.
type AlertEventKind int

const (
	// AlertStateOpened and AlertStateResolved: a state of monitoring-alert-trigger opened or
	// cleared in a state machine (CheckAlert, the T5 path).
	AlertStateOpened AlertEventKind = iota
	AlertStateResolved
	// AlertServerStateChange: a server's status changed (ServerMonitor.SendAlert).
	AlertServerStateChange
	// AlertStateLine: the OPENED/RESOLV line of a state printed to a domain log
	// (logPrintStateTo), which Slack and Teams forward.
	AlertStateLine
	// AlertLogLine: a log line at a level the log channels forward (ERROR, WARN, ALERT,
	// ALERTOK, START; LogModuleWithFieldsPrintf).
	AlertLogLine
	// AlertHookMessage: a message for the Slack hooks when the named hook (slack, cloud18)
	// is active (the send-alert API, the local-login notice).
	AlertHookMessage
)

// The domains of an alert's state (T3), as the alertmanager channel labels them.
const (
	ConstAlertDomainHA       = "ha"
	ConstAlertDomainWorkload = "workload"
	ConstAlertDomainSecurity = "security"
	ConstAlertDomainSchema   = "schema"
	ConstAlertDomainConfig   = "config"
)

// AlertEvent is one alert, with what every channel needs to render it its own way.
type AlertEvent struct {
	Kind AlertEventKind
	Time time.Time

	// Alert is the payload mail, the script and Telegram render (Instance, Cluster, Host,
	// State, PrevState, Resolved). Set for AlertStateOpened/Resolved and
	// AlertServerStateChange.
	Alert alert.Alert

	// State is the state that opened or cleared (AlertStateOpened/Resolved, AlertStateLine).
	State state.State
	// Domain is the state machine the state belongs to (ConstAlertDomain*).
	Domain string
	// Resolved is true for a state that cleared (AlertStateResolved, AlertStateLine).
	Resolved bool

	// Level is the log level of an AlertLogLine (ERROR, WARN, ALERT, ALERTOK, START) or
	// STATE for an AlertStateLine.
	Level string
	// Message is the formatted line: the log text (AlertLogLine), or the state's first
	// description line (AlertStateLine).
	Message string
	// TeamsText is the line as Teams shows it (with the cluster, module and level prefix).
	TeamsText string
	// Fields are the logrus fields the Slack and Pushover hooks read.
	Fields map[string]interface{}
	// SlackFields are the fields of the Slack hooks (channel Slack, cloud18 and client
	// when on Cloud18).
	SlackFields map[string]interface{}
	// Hook is the Slack hook an AlertHookMessage needs active.
	Hook string
	// Maintenance is true for a log line of a maintenance module (Slack and Teams skip
	// ERROR and WARN of these).
	Maintenance bool
}

// copyFields is a copy of a logrus field map: an event outlives the caller's map.
func copyFields(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	c := make(map[string]interface{}, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
