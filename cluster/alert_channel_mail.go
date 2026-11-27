// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"os/exec"
	"time"

	"github.com/signal18/replication-manager/config"
)

// mailAlertChannel is the alert email: to mail-to and the holders of alert-mail-receiver
// (GetAlertRecipients), for the states of monitoring-alert-trigger and the server state
// changes. A failed send is counted in failSendCount, which opens WARN0138 (CheckSendMail),
// as before the registry.
type mailAlertChannel struct{ cluster *Cluster }

func newMailAlertChannel(cluster *Cluster) *mailAlertChannel {
	return &mailAlertChannel{cluster: cluster}
}

func (c *mailAlertChannel) Name() string { return ConstAlertChannelMail }

func (c *mailAlertChannel) recipients() string {
	return c.cluster.GetAlertRecipients(AlertRecipient{To: c.cluster.Conf.MailTo, All: true})
}

func (c *mailAlertChannel) Enabled() bool { return c.recipients() != "" }

func (c *mailAlertChannel) Accepts(ev AlertEvent) bool {
	return isStateAlert(ev)
}

// isStateAlert: the events of the SendAlert path (mail, script, Telegram).
func isStateAlert(ev AlertEvent) bool {
	return ev.Kind == AlertStateOpened || ev.Kind == AlertStateResolved || ev.Kind == AlertServerStateChange
}

func (c *mailAlertChannel) Timeout() time.Duration {
	// the mailer has its own (mail-timeout); this bounds the worker beyond it
	return time.Duration(c.cluster.Conf.MailTimeout+10) * time.Second
}

func (c *mailAlertChannel) Send(_ context.Context, ev AlertEvent) error {
	cluster := c.cluster
	to := c.recipients()
	if to == "" {
		return nil
	}
	if cluster.Mailer == nil {
		if err := cluster.InitMailer(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not send alert: %s ", err)
			return nil
		}
	}
	a := ev.Alert
	if err := a.EmailMessage(to, cluster.Mailer); err != nil {
		if cluster.failSendCount < 3 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Could not send mail alert: %s", err)
		}
		cluster.failSendCount++
	} else {
		cluster.failSendCount = 0
	}
	return nil
}

// scriptAlertChannel runs alert-script (F6: the client's own alerting) with the cluster, the
// host, the previous and the new state, for the same events as the mail. It runs on its
// worker, killed after alert-script-timeout, no longer on the monitor tick.
type scriptAlertChannel struct{ cluster *Cluster }

func newScriptAlertChannel(cluster *Cluster) *scriptAlertChannel {
	return &scriptAlertChannel{cluster: cluster}
}

func (c *scriptAlertChannel) Name() string               { return ConstAlertChannelScript }
func (c *scriptAlertChannel) Enabled() bool              { return c.cluster.Conf.AlertScript != "" }
func (c *scriptAlertChannel) Accepts(ev AlertEvent) bool { return isStateAlert(ev) }

func (c *scriptAlertChannel) Timeout() time.Duration {
	if t := c.cluster.Conf.AlertScriptTimeout; t > 0 {
		return time.Duration(t) * time.Second
	}
	return 300 * time.Second
}

func (c *scriptAlertChannel) Send(ctx context.Context, ev AlertEvent) error {
	cluster := c.cluster
	script := cluster.Conf.AlertScript
	if script == "" {
		return nil
	}
	a := ev.Alert
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling alert script")
	out, err := exec.CommandContext(ctx, script, a.Cluster, a.Host, a.PrevState, a.State).CombinedOutput()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Alert script complete: %s", string(out))
	return nil
}
