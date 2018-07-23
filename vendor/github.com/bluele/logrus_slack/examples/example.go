package main

import (
	"github.com/bluele/logrus_slack"
	"github.com/sirupsen/logrus"
)

const (
	hookURL = "https://hooks.slack.com/TXXXXX/BXXXXX/XXXXXXXXXX"
)

func main() {
	logrus.SetLevel(logrus.DebugLevel)

	logrus.AddHook(&logrus_slack.SlackHook{
		HookURL:        hookURL,
		AcceptedLevels: logrus_slack.LevelThreshold(logrus.WarnLevel),
		Channel:        "#general",
		IconEmoji:      ":ghost:",
		Username:       "logrus_slack",
	})

	logrus.WithFields(logrus.Fields{"foo": "bar", "foo2": "bar2"}).Warn("this is a warn level message")
	logrus.Debug("this is a debug level message")
	logrus.Info("this is an info level message")
	logrus.Error("this is an error level message")
}
