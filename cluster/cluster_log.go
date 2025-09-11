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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	teams "github.com/atc0005/go-teams-notify/v2"
	"github.com/atc0005/go-teams-notify/v2/messagecard"

	"github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
	log "github.com/sirupsen/logrus"
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
	logs = strings.ReplaceAll(logs, "%", "%%")

	if err != nil && args != nil {
		cluster.LogModulePrintf(false, config.ConstLogModSQL, level, format, args...)
	}
	if logs != "" {
		if err != nil {
			cluster.LogSqlErrorPrintf(config.LvlInfo, url, err, from, logs, fmt.Sprintf(format, args...))
		}

		if cluster.Conf.IsEligibleForPrinting(config.ConstLogModSQL, level) {
			cluster.LogSqlGeneralPrintf(config.LvlInfo, url, from, logs)
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
		cluster.Logrus.Println(s)
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
			cluster.Logrus.Printf(
				"Failed to parse proxy URL %q: %v. Using the default HTTP client without a proxy.",
				webhookProxyUrl,
				err,
			)
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

	msgCard.Text = fmt.Sprintf(format, args...)
	// send
	if err := mstClient.Send(webhookUrl, msgCard); err != nil {
		cluster.Logrus.Printf(
			"failed to send MSTeams alert message: %s",
			err,
		)
	}
	return nil
}

/*
This function is for printing log based on module log level
set forcingLog = true if you want to force print
*/
func (cluster *Cluster) LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int {
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
	format = "[" + cluster.Name + "] [" + tag + "] " + padright(level, " ", 5) + " - " + format

	eligible := cluster.Conf.IsEligibleForPrinting(module, level)
	//Write to htlog and tlog
	if eligible || forcingLog {
		// line = cluster.LogPrintf(level, format, args...)
		if cluster.tlog != nil && cluster.tlog.Len > 0 {
			cluster.tlog.Add(fmt.Sprintf(format, args...))
		}

		if cluster.Conf.HttpServ {
			httpformat := fmt.Sprintf("[%s] %s", tag, cliformat)
			msg := s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     level,
				Timestamp: stamp,
				Text:      fmt.Sprintf(httpformat, args...),
			}
			line = cluster.htlog.Add(msg)
			switch module {
			case config.ConstLogModTask, config.ConstLogModSST, config.ConstLogModBackupStream:
				cluster.LogTask.Add(msg)
			default:
				cluster.Log.Add(msg)
			}
		}

		slackFields := make(log.Fields)
		slackFields["channel"] = "Slack"
		slackFields["cluster"] = cluster.Name
		slackFields["module"] = tag
		slackFields["type"] = "alert"
		if cluster.Conf.Cloud18 {
			slackFields["cloud18"] = cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
			slackFields["client"] = cluster.Conf.Cloud18GitUser
		}

		if cluster.Conf.Daemon {
			// wrap logrus levels
			switch level {
			case "ERROR":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Errorf(cliformat, args...)
				if cluster.Conf.SlackURL != "" {
					cluster.LogSlack.WithFields(slackFields).Errorf(cliformat, args...)
				}
				if cluster.Conf.TeamsUrl != "" {
					go cluster.sendMsTeams(level, format, args...)
				}
			case "INFO":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Infof(cliformat, args...)
			case "DEBUG":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Debugf(cliformat, args...)
			case "WARN":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Warnf(cliformat, args...)
				if cluster.Conf.SlackURL != "" {
					cluster.LogSlack.WithFields(slackFields).Warnf(cliformat, args...)
				}
				if cluster.Conf.TeamsUrl != "" {
					go cluster.sendMsTeams(level, format, args...)
				}
			case "TEST":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "test", "channel": "StdOut", "module": tag}).Infof(cliformat, args...)
			case "BENCH":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "benchmark", "channel": "StdOut", "module": tag}).Infof(cliformat, args...)
			case "ALERT":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "StdOut", "module": tag}).Errorf(cliformat, args...)
				if cluster.Conf.Cloud18Alert {
					cluster.LogSlack.WithFields(slackFields).Errorf(cliformat, args...)
				}
				if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
					cluster.LogPushover.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Pushover", "module": tag}).Errorf(cliformat, args...)
				}
				if cluster.Conf.TeamsUrl != "" {
					go cluster.sendMsTeams(level, format, args...)
				}
			case "START":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "StdOut", "module": tag}).Warnf(cliformat, args...)
				if cluster.LogSlack.HasActiveHook() {
					slackFields["type"] = "start"
					cluster.LogSlack.WithFields(slackFields).Warnf(cliformat, args...)
				}
				if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
					cluster.LogPushover.WithFields(log.Fields{"cluster": cluster.Name, "type": "start", "channel": "Pushover", "module": tag}).Warnf(cliformat, args...)
				}
				if cluster.Conf.TeamsUrl != "" {
					go cluster.sendMsTeams(level, format, args...)
				}
			case "ALERTOK":
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "StdOut", "module": tag}).Infof(cliformat, args...)
				if cluster.Conf.Cloud18Alert {
					cluster.LogSlack.WithFields(slackFields).Infof(cliformat, args...)
				}
				if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
					cluster.LogPushover.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert", "channel": "Pushover", "module": tag}).Infof(cliformat, args...)
				}
				if cluster.Conf.TeamsUrl != "" {
					go cluster.sendMsTeams(level, format, args...)
				}
			default:
				cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Printf(cliformat, args...)
			}
		}
	}

	return line
}

/*
This function is for printing log based on module log level
set forcingLog = true if you want to force print
*/
func (cluster *Cluster) LogTaskPrintDebug(forcingLog bool, module int, key string, format string, args ...interface{}) int {
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
	format = "[" + cluster.Name + "] [" + tag + "] " + padright(config.LvlDbg, " ", 5) + " - " + format

	eligible := cluster.Conf.IsEligibleForPrinting(module, config.LvlDbg)
	//Write to htlog and tlog
	if eligible || forcingLog {
		// line = cluster.LogPrintf(level, format, args...)
		if cluster.tlog != nil && cluster.tlog.Len > 0 {
			cluster.tlog.Add(fmt.Sprintf(format, args...))
		}

		if cluster.Conf.HttpServ {
			httpformat := fmt.Sprintf("[%s] %s", tag, cliformat)
			msg := s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     config.LvlDbg,
				Timestamp: stamp,
				Text:      fmt.Sprintf(httpformat, args...),
			}
			line = cluster.htlog.Add(msg)
			switch module {
			case config.ConstLogModTask, config.ConstLogModSST, config.ConstLogModBackupStream:
				if line2, ok := cluster.debugLineMap[key]; ok {
					cluster.LogTask.Update(line2, msg)
				} else {
					cluster.debugLineMap[key] = cluster.LogTask.Add(msg)
				}

			default:
				if line2, ok := cluster.debugLineMap[key]; ok {
					cluster.Log.Update(line2, msg)
				} else {
					cluster.debugLineMap[key] = cluster.Log.Add(msg)
				}
			}
		}

		if cluster.Conf.Daemon {
			cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "log", "module": tag}).Debugf(cliformat, args...)
		}
	}

	return line
}

/*
This function is for printing state
*/
func (cluster *Cluster) LogPrintAllStates() {
	SM := cluster.GetStateMachine()
	if cluster.runOnceAfterTopology == false {
		for _, st := range SM.GetLastResolvedStates() {
			cluster.LogPrintState(st, true)
		}
	}
	for _, st := range SM.GetLastOpenedStates() {
		cluster.LogPrintState(st, false)
	}
}

/*
This function is for printing state
*/
func (cluster *Cluster) LogPrintState(st state.State, resolved bool) int {
	var format string
	level := "STATE"
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

	if resolved {
		format = fmt.Sprintf("RESOLV %s : %s", st.ErrKey, st.ErrDesc)
	} else {
		format = fmt.Sprintf("OPENED %s : %s", st.ErrKey, st.ErrDesc)
	}

	tag := config.GetTagsForLog(config.ConstLogModGeneral)
	cliformat := format
	format = "[" + cluster.Name + "] [" + tag + "] " + padright(level, " ", 5) + " - " + format

	if cluster.tlog != nil && cluster.tlog.Len > 0 {
		cluster.tlog.Add(format)
	}

	if cluster.Conf.HttpServ {
		httpformat := fmt.Sprintf("[%s] %s", tag, cliformat)
		msg := s18log.HttpMessage{
			Group:     cluster.Name,
			Level:     level,
			Timestamp: stamp,
			Text:      fmt.Sprintf(httpformat),
		}
		line = cluster.htlog.Add(msg)
		cluster.Log.Add(msg)
	}

	slackFields := make(log.Fields)
	slackFields["channel"] = "Slack"
	slackFields["cluster"] = cluster.Name
	slackFields["type"] = "state"
	if cluster.Conf.Cloud18 {
		slackFields["cloud18"] = cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
		slackFields["client"] = cluster.Conf.Cloud18GitUser
	}

	if cluster.Conf.Daemon {
		// wrap logrus levels
		if resolved {
			cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": "RESOLV", "code": st.ErrKey, "channel": "StdOut"}).Warnf(st.ErrDesc)
			if strings.Contains(cluster.Conf.MonitoringAlertTrigger, st.ErrKey) {
				if cluster.LogSlack.HasActiveHook() {
					slackFields["status"] = "RESOLV"
					cluster.LogSlack.WithFields(slackFields).Infof(st.ErrDesc)
				}
			}
		} else {
			cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": "OPENED", "code": st.ErrKey, "channel": "StdOut"}).Warnf(st.ErrDesc)
			if strings.Contains(cluster.Conf.MonitoringAlertTrigger, st.ErrKey) {
				if cluster.LogSlack.HasActiveHook() {
					slackFields["status"] = "OPENED"
					cluster.LogSlack.WithFields(slackFields).Errorf(st.ErrDesc)
				}
			}
		}

		if cluster.Conf.TeamsUrl != "" && cluster.Conf.TeamsAlertState != "" {
			stateList := strings.Split(cluster.Conf.TeamsAlertState, ",")
			for _, alertcode := range stateList {
				if strings.Contains(st.ErrKey, alertcode) {
					go cluster.sendMsTeams(level, format)
					break
				}
			}
		}
	}

	return line
}

func (cluster *Cluster) CopyLogs(r io.Reader, module int, level string, name string) {
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else if s.Text() != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", name, strings.Replace(s.Text(), cluster.GetDbPass(), "XXXX", 1))
		}
	}
}

func (cluster *Cluster) LogPanicToFile(task string) {
	if r := recover(); r != nil {
		fields := log.Fields{
			"cluster":    cluster.Name,
			"task":       task,
			"panic":      r,
			"stacktrace": string(debug.Stack()),
		}

		cluster.Logrus.WithFields(fields).Print("Application terminated unexpectedly")

		// Convert to json
		path := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "panic.log")
		content, err := json.MarshalIndent(fields, "", "\t")
		if err != nil {
			cluster.Logrus.Print("Unable to decode stacktrace")
		}

		if err = os.WriteFile(path, content, 0644); err != nil {
			cluster.Logrus.Print("Unable to write stacktrace to panic.log")
		}

	}
}
