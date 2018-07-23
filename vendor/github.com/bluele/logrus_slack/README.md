# Slack Hooks for Logrus <img src="http://i.imgur.com/hTeVwmJ.png" width="40" height="40" alt=":walrus:" class="emoji" title=":walrus:"/>
[![GoDoc](https://godoc.org/github.com/bluele/logrus_slack?status.png)](https://godoc.org/github.com/bluele/logrus_slack)

## Example

```go
package main

import (
	"github.com/sirupsen/logrus"
	"github.com/bluele/logrus_slack"
)

const (
	// slack webhook url
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
		Timeout:        5 * time.Second, // request timeout for calling slack api
	})

	logrus.WithFields(logrus.Fields{"foo": "bar", "foo2": "bar2"}).Warn("this is a warn level message")
	logrus.Debug("this is a debug level message")
	logrus.Info("this is an info level message")
	logrus.Error("this is an error level message")
}
```

## Install

```
$ go get -u github.com/bluele/logrus_slack
```

## Credits 

This project based on [slackrus](https://github.com/johntdyer/slackrus)

## Author

**Jun Kimura**

* <http://github.com/bluele>
* <junkxdev@gmail.com>
