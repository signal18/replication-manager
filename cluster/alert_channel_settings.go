// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/alert/telegram"
)

// The settings of the alert channels added with the registry, as the settings API sets them
// (server/api_cluster.go). A channel reads them at each alert: a change applies at the next.

// SetAlertChannelSecret sets a secret of an alert channel (alert-alertmanager-password,
// alert-alertmanager-bearer-token, alert-telegram-bot-token) through the credential chain,
// so it is encrypted in the saved config (F9) and masked on the API (F10). Empty clears it.
func (cluster *Cluster) SetAlertChannelSecret(key, value string) error {
	value = strings.TrimSpace(value)
	var field *string
	switch key {
	case "alert-alertmanager-password":
		field = &cluster.Conf.AlertAlertmanagerPassword
	case "alert-alertmanager-bearer-token":
		field = &cluster.Conf.AlertAlertmanagerBearerToken
	case "alert-telegram-bot-token":
		field = &cluster.Conf.AlertTelegramBotToken
	default:
		return fmt.Errorf("%s is not an alert channel secret", key)
	}
	if err := cluster.SetCredential(key, value); err != nil {
		return err
	}
	*field = value
	cluster.MarkSecretVersionStoreDirty()
	return nil
}

// SetAlertChannelSetting sets a plain string setting of an alert channel.
func (cluster *Cluster) SetAlertChannelSetting(key, value string) error {
	value = strings.TrimSpace(value)
	conf := cluster.Conf
	switch key {
	case "alert-alertmanager-url":
		for _, u := range strings.Split(value, ",") {
			if u = strings.TrimSpace(u); u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return fmt.Errorf("alert-alertmanager-url: %q is not an http(s) URL", u)
			}
		}
		conf.AlertAlertmanagerURL = value
	case "alert-alertmanager-user":
		conf.AlertAlertmanagerUser = value
	case "alert-telegram-chat-id":
		conf.AlertTelegramChatID = value
	case "alert-telegram-parse-mode":
		if value != "" && value != telegram.ParseModeMarkdownV2 {
			return fmt.Errorf("alert-telegram-parse-mode must be empty (plain text) or %s", telegram.ParseModeMarkdownV2)
		}
		conf.AlertTelegramParseMode = value
	case "alert-telegram-api-url":
		if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return fmt.Errorf("alert-telegram-api-url: %q is not an http(s) URL", value)
		}
		conf.AlertTelegramAPIURL = value
	default:
		return fmt.Errorf("%s is not an alert channel setting", key)
	}
	return nil
}

// SetAlertChannelInt sets a numeric setting of the alert channels; <= 0 falls back to the
// channel's default at use.
func (cluster *Cluster) SetAlertChannelInt(key, value string) error {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: %q is not a number", key, value)
	}
	conf := cluster.Conf
	switch key {
	case "alert-queue-limit":
		conf.AlertQueueLimit = v
	case "alert-send-timeout":
		conf.AlertSendTimeout = v
	case "alert-script-timeout":
		conf.AlertScriptTimeout = v
	case "alert-alertmanager-timeout":
		conf.AlertAlertmanagerTimeout = v
	case "alert-alertmanager-resend-interval":
		conf.AlertAlertmanagerResendInterval = v
	case "alert-telegram-timeout":
		conf.AlertTelegramTimeout = v
	default:
		return fmt.Errorf("%s is not an alert channel setting", key)
	}
	return nil
}
