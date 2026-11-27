// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	teams "github.com/atc0005/go-teams-notify/v2"
	"github.com/atc0005/go-teams-notify/v2/messagecard"
	log "github.com/sirupsen/logrus"
)

// The chat channels forward log lines and state lines, as the logrus hooks did before the
// registry: Slack (the alert-slack-url hook and the Cloud18 Mattermost-compatible hook,
// both in cluster.LogSlack), Pushover (cluster.LogPushover) and Teams. Each Accepts keeps the
// rule its call site had in LogModuleWithFieldsPrintf and logPrintStateTo; the intervention
// check stays at the call site.

func (cluster *Cluster) inAlertTrigger(code string) bool {
	return strings.Contains(cluster.Conf.MonitoringAlertTrigger, code)
}

// slackAlertChannel fires the active Slack hooks (slack, cloud18).
type slackAlertChannel struct{ cluster *Cluster }

func newSlackAlertChannel(cluster *Cluster) *slackAlertChannel {
	return &slackAlertChannel{cluster: cluster}
}

func (c *slackAlertChannel) Name() string           { return ConstAlertChannelSlack }
func (c *slackAlertChannel) Enabled() bool          { return c.cluster.LogSlack != nil }
func (c *slackAlertChannel) Timeout() time.Duration { return 10 * time.Second } // each hook has its 5s

func (c *slackAlertChannel) Accepts(ev AlertEvent) bool {
	if c.cluster.LogSlack == nil {
		return false
	}
	conf := c.cluster.Conf
	switch ev.Kind {
	case AlertLogLine:
		switch ev.Level {
		case "ERROR", "WARN":
			return !ev.Maintenance && conf.SlackURL != ""
		case "ALERT", "ALERTOK":
			return conf.Cloud18Alert && conf.Cloud18SubscriptionPlan != "free"
		case "START":
			return c.cluster.LogSlack.HasActiveHook()
		}
	case AlertStateLine:
		return c.cluster.inAlertTrigger(ev.State.ErrKey) && c.cluster.LogSlack.HasActiveHook()
	case AlertHookMessage:
		return c.cluster.LogSlack.IsHookActive(ev.Hook)
	}
	return false
}

func (c *slackAlertChannel) Send(_ context.Context, ev AlertEvent) error {
	entry := c.cluster.LogSlack.WithFields(ev.SlackFields).WithTime(ev.Time)
	switch ev.Kind {
	case AlertStateLine:
		if ev.Resolved {
			entry.Info(ev.Message)
		} else {
			entry.Error(ev.Message)
		}
	case AlertHookMessage:
		entry.Warn(ev.Message)
	default:
		switch ev.Level {
		case "ERROR", "ALERT":
			entry.Error(ev.Message)
		case "WARN", "START":
			entry.Warn(ev.Message)
		case "ALERTOK":
			entry.Info(ev.Message)
		}
	}
	return nil
}

// SendAlertHookMessage sends msg to the Slack hooks when the hook named (slack, cloud18) is
// active; false when it is not. The send-alert API and the local-login notice.
func (cluster *Cluster) SendAlertHookMessage(hook string, fields map[string]interface{}, msg string) bool {
	if cluster.LogSlack == nil || !cluster.LogSlack.IsHookActive(hook) {
		return false
	}
	return cluster.dispatchAlert(AlertEvent{Kind: AlertHookMessage, Hook: hook, Message: msg, SlackFields: copyFields(fields)}) > 0
}

// pushoverAlertChannel fires the Pushover hook for the ALERT, ALERTOK and START lines.
type pushoverAlertChannel struct {
	cluster *Cluster
	// inFlight: the Pushover client has no deadline (http.DefaultClient). A send that
	// outlives the timeout keeps running, alone: the next one waits for it to end instead
	// of starting a second goroutine, so a hung API never piles up goroutines.
	inFlight atomic.Bool
}

func newPushoverAlertChannel(cluster *Cluster) *pushoverAlertChannel {
	return &pushoverAlertChannel{cluster: cluster}
}

func (c *pushoverAlertChannel) Name() string { return ConstAlertChannelPushover }

func (c *pushoverAlertChannel) Enabled() bool {
	conf := c.cluster.Conf
	return c.cluster.LogPushover != nil && conf.PushoverAppToken != "" && conf.PushoverUserToken != ""
}

func (c *pushoverAlertChannel) Accepts(ev AlertEvent) bool {
	return ev.Kind == AlertLogLine && (ev.Level == "ALERT" || ev.Level == "ALERTOK" || ev.Level == "START")
}

func (c *pushoverAlertChannel) Timeout() time.Duration { return alertSendTimeout(c.cluster) }

func (c *pushoverAlertChannel) Send(ctx context.Context, ev AlertEvent) error {
	if !c.inFlight.CompareAndSwap(false, true) {
		return errors.New("the previous Pushover send has not ended")
	}
	logger := c.cluster.LogPushover
	done := make(chan struct{})
	go func() {
		defer c.inFlight.Store(false)
		defer close(done)
		entry := logger.WithFields(log.Fields(ev.Fields)).WithTime(ev.Time)
		switch ev.Level {
		case "ALERT":
			entry.Error(ev.Message)
		case "START":
			entry.Warn(ev.Message)
		case "ALERTOK":
			entry.Info(ev.Message)
		}
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("Pushover send timed out")
	}
}

func alertSendTimeout(cluster *Cluster) time.Duration {
	if t := cluster.Conf.AlertSendTimeout; t > 0 {
		return time.Duration(t) * time.Second
	}
	return 10 * time.Second
}

// teamsAlertChannel posts a message card to alert-teams-url.
type teamsAlertChannel struct{ cluster *Cluster }

func newTeamsAlertChannel(cluster *Cluster) *teamsAlertChannel {
	return &teamsAlertChannel{cluster: cluster}
}

func (c *teamsAlertChannel) Name() string           { return ConstAlertChannelTeams }
func (c *teamsAlertChannel) Enabled() bool          { return c.cluster.Conf.TeamsUrl != "" }
func (c *teamsAlertChannel) Timeout() time.Duration { return alertSendTimeout(c.cluster) }

func (c *teamsAlertChannel) Accepts(ev AlertEvent) bool {
	conf := c.cluster.Conf
	switch ev.Kind {
	case AlertLogLine:
		switch ev.Level {
		case "ERROR", "WARN":
			return !ev.Maintenance
		case "ALERT", "ALERTOK", "START":
			return true
		}
	case AlertStateLine:
		if conf.TeamsAlertState == "" {
			return false
		}
		for _, code := range strings.Split(conf.TeamsAlertState, ",") {
			if strings.Contains(ev.State.ErrKey, code) {
				return true
			}
		}
	}
	return false
}

func (c *teamsAlertChannel) Send(ctx context.Context, ev AlertEvent) error {
	c.cluster.sendMsTeams(ctx, ev.Level, ev.TeamsText)
	return nil
}

func (cluster *Cluster) sendMsTeams(ctx context.Context, level string, text string) {
	// init the client
	mstClient := teams.NewTeamsClient()

	// setup webhook url
	webhookUrl := cluster.Conf.TeamsUrl
	webhookProxyUrl := cluster.Conf.TeamsProxyUrl

	proxyUrl, err := url.Parse(webhookProxyUrl)

	// Create a copy of the default mstClient.HTTPClient
	httpClient := mstClient.HTTPClient()

	if webhookProxyUrl != "" {
		if err == nil {
			httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(proxyUrl)}
		} else {
			// the proxy URL may carry credentials: log the parse error only
			cluster.Logrus.Printf("Failed to parse alert-teams-proxy-url. Using the default HTTP client without a proxy.")
		}
	} else {
		cluster.Logrus.Printf("Proxy URL is empty. Using the default HTTP client without a proxy.")
	}

	// setup message card
	msgCard := messagecard.NewMessageCard()
	msgCard.Title = "Replication-Manager alert. Monitor: " + cluster.Conf.MonitorAddress
	switch level {
	case "ERROR":
		msgCard.ThemeColor = "#4169e1"
	case "ALERT":
		msgCard.ThemeColor = "#b22222"
	case "WARN":
		msgCard.ThemeColor = "#112233"
	}

	msgCard.Text = text
	// send
	if err := mstClient.SendWithContext(ctx, webhookUrl, msgCard); err != nil {
		// the client's errors quote the webhook URL, the credential
		msg := err.Error()
		if webhookUrl != "" {
			msg = strings.ReplaceAll(msg, webhookUrl, "<alert-teams-url>")
		}
		cluster.Logrus.Printf("failed to send MSTeams alert message: %s", msg)
	}
}
