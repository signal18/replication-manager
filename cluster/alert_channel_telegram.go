// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/alert/telegram"
)

// telegramAlertChannel sends the alert email's text through a Telegram bot, for the same
// events as the mail: the states of monitoring-alert-trigger and the server state changes.
// The bot token is a secret (F9) and never in a log or an error (F10): the client redacts
// it from every error, the request URL holding it.
type telegramAlertChannel struct{ cluster *Cluster }

func newTelegramAlertChannel(cluster *Cluster) *telegramAlertChannel {
	return &telegramAlertChannel{cluster: cluster}
}

func (c *telegramAlertChannel) Name() string { return ConstAlertChannelTelegram }

func (c *telegramAlertChannel) Enabled() bool {
	conf := c.cluster.Conf
	return conf.AlertTelegram && conf.AlertTelegramBotToken != "" && strings.TrimSpace(conf.AlertTelegramChatID) != ""
}

func (c *telegramAlertChannel) Accepts(ev AlertEvent) bool { return isStateAlert(ev) }

func (c *telegramAlertChannel) Timeout() time.Duration {
	if t := c.cluster.Conf.AlertTelegramTimeout; t > 0 {
		return time.Duration(t) * time.Second
	}
	return 10 * time.Second
}

func (c *telegramAlertChannel) Send(ctx context.Context, ev AlertEvent) error {
	conf := c.cluster.Conf
	client := telegram.New(conf.AlertTelegramAPIURL, conf.GetDecryptedValue("alert-telegram-bot-token"), conf.AlertTelegramChatID, conf.AlertTelegramParseMode, c.Timeout())
	a := ev.Alert
	return client.Send(ctx, client.Format(a.Subject(), strings.TrimRight(a.Text(), "\n")))
}
