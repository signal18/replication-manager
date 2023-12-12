// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// cluster_log.go
package cluster

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	teams "github.com/atc0005/go-teams-notify/v2"
	"github.com/atc0005/go-teams-notify/v2/messagecard"

	"github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/utils/s18log"
	log "github.com/sirupsen/logrus"
)

// Log levels
const (
	LvlInfo = "INFO"
	LvlWarn = "WARN"
	LvlErr  = "ERROR"
	LvlDbg  = "DEBUG"
)

// State Levels
const (
	StateWarn = "WARNING"
	StateErr  = "ERROR"
)

func (cluster *Cluster) display() {
	if cluster.Name != cluster.cfgGroupDisplay {
		return
	}

	cluster.tlog.Print()

}

func (cluster *Cluster) LogSQL(logs string, err error, url string, from string, level string, format string, args ...interface{}) {
	if err != nil && args != nil {
		cluster.LogPrintf(level, format, args)
	}
	if logs != "" {
		if err != nil {
			cluster.LogSqlErrorPrintf(LvlInfo, url, err, from, logs, fmt.Sprintf(format, args...))
		}
		if from != "Monitor" {
			cluster.LogSqlGeneralPrintf(LvlInfo, url, from, logs)
		} else if cluster.Conf.LogSQLInMonitoring {
			cluster.LogSqlGeneralPrintf(LvlInfo, url, from, logs)
		}
	}
}

func (cluster *Cluster) printTb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func (cluster *Cluster) printfTb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	cluster.printTb(x, y, fg, bg, s)
}

func (cluster *Cluster) LogPrint(msg ...interface{}) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))

	if cluster.Conf.LogFile != "" {
		s := fmt.Sprint(stamp, " [", cluster.Name, "] ", fmt.Sprint(msg...))
		io.WriteString(cluster.logPtr, fmt.Sprintln(s))
	}
	if cluster.tlog.Len > 0 {
		s := fmt.Sprint("[", cluster.Name, "] ", fmt.Sprint(msg...))
		cluster.tlog.Add(s)
		cluster.display()
	}
	if cluster.Conf.Daemon {
		s := fmt.Sprint("[", cluster.Name, "] ", fmt.Sprint(msg...))
		log.Println(s)
	}
}

func (cluster *Cluster) LogSqlGeneralPrintf(level string, url string, from string, format string) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	msg := s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     level,
		Timestamp: stamp,
		Text:      format,
	}
	cluster.SQLGeneralLog.Add(msg)
	cluster.SqlGeneralLog.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from}).Infof(format)
}

func (cluster *Cluster) LogSqlErrorPrintf(level string, url string, err error, from string, logs string, format string) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	msg := s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     level,
		Timestamp: stamp,
		Text:      logs,
	}
	cluster.SQLErrorLog.Add(msg)
	cluster.SqlErrorLog.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from, "error": err, "sql": logs}).Errorf(format)
}

func (cluster *Cluster) LogUpdate(line int, level string, format string, args ...interface{}) int {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))

	msg := s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     level,
		Timestamp: stamp,
		Text:      fmt.Sprintf(format, args...),
	}
	cluster.Log.Update(line, msg)
	return line
}

func (cluster *Cluster) LogPrintf(level string, format string, args ...interface{}) int {
	//fmt.Printf("CLUSTER LOGPRINTF %s :"+format, level, args)
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
	cliformat := format
	format = "[" + cluster.Name + "] " + padright(level, " ", 5) + " - " + format
	if level == "DEBUG" && !cluster.Conf.HasLogLevelType("log-debug") && !cluster.Conf.Verbose {
		// Only print debug messages if loglevel > 1
	} else {
		// if cluster.Conf.LogFile != "" {
		// 	//			f := fmt.Sprintln(stamp, format)

		// 	//	io.WriteString(cluster.logPtr, fmt.Sprintf(f, args...))
		// 	//	log.WithField("cluster", cluster.Name).Debugf(cliformat, args...)
		// }
		if cluster.tlog != nil && cluster.tlog.Len > 0 {
			cluster.tlog.Add(fmt.Sprintf(format, args...))
			//		cluster.display()
		}

		if cluster.Conf.HttpServ {
			msg := s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     level,
				Timestamp: stamp,
				Text:      fmt.Sprintf(cliformat, args...),
			}
			line = cluster.htlog.Add(msg)
			cluster.Log.Add(msg)
		}
	}

	if cluster.Conf.Daemon {
		// wrap logrus levels
		switch level {
		case "ERROR":
			log.WithField("cluster", cluster.Name).Errorf(cliformat, args...)
			if cluster.Conf.SlackURL != "" {
				cluster.LogSlack.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Slack"}).Errorf(cliformat, args...)
			}
			if cluster.Conf.TeamsUrl != "" {
				go cluster.sendMsTeams(level, format, args)
			}
		case "INFO":
			log.WithField("cluster", cluster.Name).Infof(cliformat, args...)
		case "DEBUG":
			log.WithField("cluster", cluster.Name).Debugf(cliformat, args...)
		case "WARN":
			log.WithField("cluster", cluster.Name).Warnf(cliformat, args...)
			if cluster.Conf.SlackURL != "" {
				cluster.LogSlack.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Slack"}).Warnf(cliformat, args...)
			}
			if cluster.Conf.TeamsUrl != "" {
				go cluster.sendMsTeams(level, format, args...)
			}
		case "TEST":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "test", "channel": "StdOut"}).Infof(cliformat, args...)
		case "BENCH":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "benchmark", "channel": "StdOut"}).Infof(cliformat, args...)
		case "ALERT":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "StdOut"}).Errorf(cliformat, args...)
			if cluster.Conf.SlackURL != "" {
				cluster.LogSlack.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Slack"}).Errorf(cliformat, args...)
			}
			if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
				cluster.LogPushover.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Pushover"}).Errorf(cliformat, args...)
			}
			if cluster.Conf.TeamsUrl != "" {
				go cluster.sendMsTeams(level, format, args...)
			}
		case "START":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "StdOut"}).Warnf(cliformat, args...)
			if cluster.Conf.SlackURL != "" {
				cluster.LogSlack.WithFields(log.Fields{"cluster": cluster.Name, "type": "start", "channel": "Slack"}).Warnf(cliformat, args...)
			}
			if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
				cluster.LogPushover.WithFields(log.Fields{"cluster": cluster.Name, "type": "start", "channel": "Pushover"}).Warnf(cliformat, args...)
			}
			if cluster.Conf.TeamsUrl != "" {
				go cluster.sendMsTeams(level, format, args...)
			}
		case "STATE":
			status := cliformat[0:6]
			code := cliformat[7:15]
			err := cliformat[18:]
			if status == "OPENED" {
				log.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": status, "code": code, "channel": "StdOut"}).Warnf(err, args...)
			} else {
				log.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": status, "code": code, "channel": "StdOut"}).Warnf(err, args...)
			}

			if cluster.Conf.TeamsUrl != "" && cluster.Conf.TeamsAlertState != "" {
				stateList := strings.Split(cluster.Conf.TeamsAlertState, ",")
				for _, alertcode := range stateList {
					if strings.Contains(code, alertcode) {
						go cluster.sendMsTeams(level, format, args...)
						break
					}
				}
			}

		default:
			log.Printf(cliformat, args...)
		}

	}

	return line
}

func (cluster *Cluster) sendMsTeams(level string, format string, args ...interface{}) error {
	// init the client
	mstClient := teams.NewTeamsClient()

	// setup webhook url
	webhookUrl := cluster.Conf.TeamsUrl
	webhookProxyUrl := cluster.Conf.TeamsProxyUrl

	proxyUrl, err := url.Parse(cluster.Conf.TeamsProxyUrl)

	// Create a copy of the default mstClient.HTTPClient
	httpClient := mstClient.HTTPClient()

	if webhookProxyUrl != "" {
		if err == nil {
			httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(proxyUrl)}
		} else {
			log.Printf(
				"Failed to parse proxy URL %q: %v. Using the default HTTP client without a proxy.",
				webhookProxyUrl,
				err,
			)
		}
	} else {
		log.Printf("Proxy URL is empty. Using the default HTTP client without a proxy.")
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

	msgCard.Text = fmt.Sprintf(format, args...)
	// send
	if err := mstClient.Send(webhookUrl, msgCard); err != nil {
		log.Printf(
			"failed to send MSTeams alert message: %s",
			err,
		)
	}
	return nil
}
