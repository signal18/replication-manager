package server

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

// State Levels
const (
	StateWarn = "WARNING"
	StateErr  = "ERROR"
)

/*
This function is for printing log based on module log level
set forcingLog = true if you want to force print
*/
func (repman *ReplicationManager) LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int {
	line := 0
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	padright := func(str, pad string, lenght int) string {
		for {
			str += pad
			if len(str) > lenght {
				return str[0:lenght]
			}
		}
	}

	tag := config.GetTagsForLog(module)
	cliformat := format
	format = "[monitor] [" + tag + "] " + padright(level, " ", 5) + " - " + format

	eligible := repman.Conf.IsEligibleForPrinting(module, level)
	//Write to htlog and tlog
	if eligible || forcingLog {
		// line = repman.LogPrintf(level, format, args...)
		if repman.tlog.Len > 0 {
			repman.tlog.Add(fmt.Sprintf(format, args...))
		}

		if repman.Conf.HttpServ {
			httpformat := fmt.Sprintf("[%s] %s", tag, cliformat)
			msg := s18log.HttpMessage{
				Group:     "none",
				Level:     level,
				Timestamp: stamp,
				Text:      fmt.Sprintf(httpformat, args...),
			}
			line = repman.Logs.Add(msg)
		}

		if repman.Conf.Daemon {
			// wrap logrus levels
			switch level {
			case "ERROR":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "log", "module": tag}).Errorf(cliformat, args...)
			case "INFO":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "log", "module": tag}).Infof(cliformat, args...)
			case "DEBUG":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "log", "module": tag}).Debugf(cliformat, args...)
			case "WARN":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "log", "module": tag}).Warnf(cliformat, args...)
			case "TEST":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "test", "channel": "StdOut", "module": tag}).Infof(cliformat, args...)
			case "BENCH":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "benchmark", "channel": "StdOut", "module": tag}).Infof(cliformat, args...)
			case "ALERT":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "alert", "channel": "StdOut", "module": tag}).Errorf(cliformat, args...)
			case "START":
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "alert", "channel": "StdOut", "module": tag}).Warnf(cliformat, args...)
			case "STATE":
				status := cliformat[0:6]
				code := cliformat[7:15]
				err := cliformat[18:]
				if status == "OPENED" {
					repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "state", "status": status, "code": code, "channel": "StdOut"}).Warnf(err, args...)
				} else {
					repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "state", "status": status, "code": code, "channel": "StdOut"}).Warnf(err, args...)
				}

			default:
				repman.Logrus.WithFields(log.Fields{"cluster": "none", "type": "log", "module": tag}).Printf(cliformat, args...)
			}
		}
	}

	return line
}

func (repman *ReplicationManager) LogPanicToFile(r interface{}) {
	repman.Logrus.WithFields(logrus.Fields{
		"panic":      r,
		"stacktrace": string(debug.Stack()),
	}).Error("Application terminated unexpectedly")
}

// Function to update the log level for the RotateFileHook
func (repman *ReplicationManager) UpdateFileHookLogLevel(hook *s18log.RotateFileHook, newLogLevel int) error {
	// Update the log level in the hook's configuration
	hook.Config.Level = config.ToLogrusLevel(newLogLevel)
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	text := fmt.Sprintf("File log level changed successfully to %s", hook.Config.Level.String())

	if repman.tlog.Len > 0 {
		repman.tlog.Add(text)
	}

	if repman.Conf.HttpServ {
		msg := s18log.HttpMessage{
			Group:     "none",
			Level:     "INFO",
			Timestamp: stamp,
			Text:      text,
		}

		for _, cl := range repman.Clusters {
			cl.Log.Add(msg)
		}
	}

	repman.Logrus.WithFields(log.Fields{"new_file_log_level": hook.Config.Level.String()}).Info(text)

	return nil
}
