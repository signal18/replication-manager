package pushover

import (
	"fmt"

	client "github.com/gregdel/pushover"
	"github.com/siddontang/go/log"
	"github.com/sirupsen/logrus"
)

type PushoverHook struct {
	app            *client.Pushover
	recipient      *client.Recipient
	AcceptedLevels []logrus.Level
}

/*
	TODO: We need to define if we want to match specific Logrus levels
	to specific Pushover priorities. They range from -2 to 2
*/

var defaultLevels []logrus.Level = []logrus.Level{
	logrus.PanicLevel,
	logrus.FatalLevel,
	logrus.ErrorLevel,
	logrus.WarnLevel,
}

func (p *PushoverHook) Levels() []logrus.Level {
	if p.AcceptedLevels == nil {
		return defaultLevels
	}

	return p.AcceptedLevels
}

// NewHook returns a Logrus.Hook for pushing messages to Pushover.
// Implements the gregdel/pushover package
func NewHook(appToken, recipientToken string) *PushoverHook {
	p := &PushoverHook{}
	p.app = client.New(appToken)
	p.recipient = client.NewRecipient(recipientToken)

	return p
}

func (p *PushoverHook) Fire(entry *logrus.Entry) error {
	pr := -1
	if entry.Level == log.LevelError {
		pr = 0
	}
	if entry.Data["type"].(string) == "alert" {
		pr = 1
	}
	message := &client.Message{
		Message:   entry.Message,
		Timestamp: entry.Time.Unix(),
		Title:     title,
		Priority:  pr,
	}
	_, err := p.app.SendMessage(message, p.recipient)
	if err != nil {
		return fmt.Errorf("Could not send message to Pushover API: %s", err)
	}
	return nil
}
