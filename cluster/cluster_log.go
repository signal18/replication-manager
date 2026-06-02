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

func (cluster *Cluster) LogPrint(msg ...any) {
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
	cluster.SqlGeneralLog.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from}).Info(format)
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
	cluster.SqlErrorLog.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from, "error": err, "sql": logs}).Error(format)
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
	return cluster.LogModuleWithFieldsPrintf(forcingLog, module, level, nil, format, args...)
}

func (cluster *Cluster) LogModuleWithFieldsPrintf(forcingLog bool, module int, level string, fields map[string]interface{}, format string, args ...interface{}) int {
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
	printfields := make(log.Fields)
	printfields["cluster"] = cluster.Name
	printfields["type"] = "log"
	printfields["module"] = tag
	for k, v := range fields {
		printfields[k] = v
	}

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
			case config.ConstLogModTask, config.ConstLogModSST, config.ConstLogModBackupStream, config.ConstLogModDbErrors, config.ConstLogModDbSqlErrors, config.ConstLogModDbSlowquery, config.ConstLogModDbOptimize, config.ConstLogModDbAudit, config.ConstLogModRestic:
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
		for k, v := range fields {
			slackFields[k] = v
		}
		if cluster.Conf.Cloud18 {
			slackFields["cloud18"] = cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
			slackFields["client"] = cluster.Conf.Cloud18GitUser
		}

		if cluster.Conf.Daemon {
			// Route to dedicated logger when configured:
			//   ConstLogModMaintenance + maintenance-adjacent modules → MaintenanceLogrus
			// ALERT/ALERTOK/START always go to the main logger regardless of module
			// since they represent operational events the on-call operator must see.
			isMaintenance := module == config.ConstLogModMaintenance ||
				module == config.ConstLogModTask ||
				module == config.ConstLogModRestic ||
				module == config.ConstLogModSST ||
				module == config.ConstLogModBackupStream ||
				module == config.ConstLogModPurge
			targetLogger := cluster.Logrus
			if isMaintenance && cluster.MaintenanceLogrus != nil {
				targetLogger = cluster.MaintenanceLogrus
			}

			// wrap logrus levels
			switch level {
			case "ERROR":
				targetLogger.WithFields(printfields).Errorf(cliformat, args...)
				if !isMaintenance && !cluster.IsIntervention {
					if cluster.Conf.SlackURL != "" {
						cluster.LogSlack.WithFields(slackFields).Errorf(cliformat, args...)
					}
					if cluster.Conf.TeamsUrl != "" {
						go cluster.sendMsTeams(level, format, args...)
					}
				} else if cluster.IsIntervention {
					cluster.IncrementSuppressedAlerts()
				}
			case "INFO":
				targetLogger.WithFields(printfields).Infof(cliformat, args...)
			case "DEBUG":
				targetLogger.WithFields(printfields).Debugf(cliformat, args...)
			case "WARN":
				targetLogger.WithFields(printfields).Warnf(cliformat, args...)
				if !isMaintenance && !cluster.IsIntervention {
					if cluster.Conf.SlackURL != "" {
						cluster.LogSlack.WithFields(slackFields).Warnf(cliformat, args...)
					}
					if cluster.Conf.TeamsUrl != "" {
						go cluster.sendMsTeams(level, format, args...)
					}
				} else if cluster.IsIntervention {
					cluster.IncrementSuppressedAlerts()
				}
			case "TEST":
				printfields["type"] = "test"
				printfields["channel"] = "StdOut"
				targetLogger.WithFields(printfields).Infof(cliformat, args...)
			case "BENCH":
				printfields["type"] = "benchmark"
				printfields["channel"] = "StdOut"
				targetLogger.WithFields(printfields).Infof(cliformat, args...)
			case "ALERT":
				printfields["type"] = "alert"
				printfields["channel"] = "StdOut"
				cluster.Logrus.WithFields(printfields).Errorf(cliformat, args...)
				if !cluster.IsIntervention {
					if cluster.Conf.Cloud18Alert && cluster.Conf.Cloud18SubscriptionPlan != "free" {
						cluster.LogSlack.WithFields(slackFields).Errorf(cliformat, args...)
					}
					if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
						printfields["channel"] = "Pushover"
						cluster.LogPushover.WithFields(printfields).Errorf(cliformat, args...)
					}
					if cluster.Conf.TeamsUrl != "" {
						go cluster.sendMsTeams(level, format, args...)
					}
				} else {
					cluster.IncrementSuppressedAlerts()
				}
			case "START":
				printfields["type"] = "alert"
				printfields["channel"] = "StdOut"
				cluster.Logrus.WithFields(printfields).Warnf(cliformat, args...)
				if !cluster.IsIntervention {
					if cluster.LogSlack.HasActiveHook() {
						slackFields["type"] = "start"
						cluster.LogSlack.WithFields(slackFields).Warnf(cliformat, args...)
					}
					if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
						printfields["type"] = "start"
						printfields["channel"] = "Pushover"
						cluster.LogPushover.WithFields(printfields).Warnf(cliformat, args...)
					}
					if cluster.Conf.TeamsUrl != "" {
						go cluster.sendMsTeams(level, format, args...)
					}
				} else {
					cluster.IncrementSuppressedAlerts()
				}
			case "ALERTOK":
				printfields["type"] = "alert"
				printfields["channel"] = "StdOut"
				cluster.Logrus.WithFields(printfields).Infof(cliformat, args...)
				if !cluster.IsIntervention {
					if cluster.Conf.Cloud18Alert && cluster.Conf.Cloud18SubscriptionPlan != "free" {
						cluster.LogSlack.WithFields(slackFields).Infof(cliformat, args...)
					}
					if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
						cluster.LogPushover.WithFields(printfields).Infof(cliformat, args...)
					}
					if cluster.Conf.TeamsUrl != "" {
						go cluster.sendMsTeams(level, format, args...)
					}
				} else {
					cluster.IncrementSuppressedAlerts()
				}
			default:
				targetLogger.WithFields(printfields).Printf(cliformat, args...)
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
	if !cluster.runOnceAfterTopology {
		for _, st := range SM.GetLastResolvedStates() {
			cluster.LogPrintState(st, true)
		}
	}
	for _, st := range SM.GetLastOpenedStates() {
		cluster.LogPrintState(st, false)
	}
}

func (cluster *Cluster) LogPrintAllWorkloadStates() {
	SM := cluster.WorkloadStateMachine
	if !cluster.runOnceAfterTopology {
		for _, st := range SM.GetLastResolvedStates() {
			cluster.logPrintStateTo(st, true, &cluster.LogWorkload)
		}
	}
	for _, st := range SM.GetLastOpenedStates() {
		cluster.logPrintStateTo(st, false, &cluster.LogWorkload)
	}
}

func (cluster *Cluster) LogPrintAllSecurityStates() {
	SM := cluster.SecurityStateMachine
	if !cluster.runOnceAfterTopology {
		for _, st := range SM.GetLastResolvedStates() {
			cluster.logPrintStateTo(st, true, &cluster.LogSecurity)
		}
	}
	for _, st := range SM.GetLastOpenedStates() {
		cluster.logPrintStateTo(st, false, &cluster.LogSecurity)
	}
}

/*
This function is for printing state
*/
func (cluster *Cluster) LogPrintState(st state.State, resolved bool) int {
	return cluster.logPrintStateTo(st, resolved, cluster.htlog)
}

func (cluster *Cluster) logPrintStateTo(st state.State, resolved bool, buf *s18log.HttpLog) int {
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
		format = "RESOLV %s : %s"
	} else {
		format = "OPENED %s : %s"
	}

	tag := config.GetTagsForLog(config.ConstLogModGeneral)
	logformat := "[%s] [%s] %s - " + format
	logargs := []interface{}{cluster.Name, tag, padright(level, " ", 5), st.ErrKey, st.ErrDesc}

	if cluster.tlog != nil && cluster.tlog.Len > 0 {
		cluster.tlog.Add(fmt.Sprintf(logformat, logargs...))
	}

	if cluster.Conf.HttpServ {
		httpmsg := fmt.Sprintf("[%s] %s", tag, fmt.Sprintf(format, st.ErrKey, st.ErrDesc))
		msg := s18log.HttpMessage{
			Group:     cluster.Name,
			Level:     level,
			Timestamp: stamp,
			Text:      httpmsg,
		}
		line = buf.Add(msg)
		// Only mirror to the general log when writing to the general buffer.
		// Workload/security states have their own buffers and must not bleed
		// into the general cluster log.
		if buf == cluster.htlog {
			cluster.Log.Add(msg)
		}
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
			cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": "RESOLV", "code": st.ErrKey, "channel": "StdOut"}).Warn(st.ErrDesc)
			if !cluster.IsIntervention && strings.Contains(cluster.Conf.MonitoringAlertTrigger, st.ErrKey) {
				if cluster.LogSlack.HasActiveHook() {
					slackFields["status"] = "RESOLV"
					cluster.LogSlack.WithFields(slackFields).Info(st.ErrDesc)
				}
			}
		} else {
			cluster.Logrus.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": "OPENED", "code": st.ErrKey, "channel": "StdOut"}).Warn(st.ErrDesc)
			if !cluster.IsIntervention && strings.Contains(cluster.Conf.MonitoringAlertTrigger, st.ErrKey) {
				if cluster.LogSlack.HasActiveHook() {
					slackFields["status"] = "OPENED"
					cluster.LogSlack.WithFields(slackFields).Error(st.ErrDesc)
				}
			}
		}

		if !cluster.IsIntervention && cluster.Conf.TeamsUrl != "" && cluster.Conf.TeamsAlertState != "" {
			stateList := strings.Split(cluster.Conf.TeamsAlertState, ",")
			for _, alertcode := range stateList {
				if strings.Contains(st.ErrKey, alertcode) {
					go cluster.sendMsTeams(level, logformat, logargs...)
					break
				}
			}
		}

		if cluster.IsIntervention && (strings.Contains(cluster.Conf.MonitoringAlertTrigger, st.ErrKey) || (cluster.Conf.TeamsUrl != "" && cluster.Conf.TeamsAlertState != "")) {
			cluster.IncrementSuppressedAlerts()
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

func (cluster *Cluster) ConsumeMessageChan() {
	for msg := range cluster.MessageChan {
		cluster.LogModuleWithFieldsPrintf(cluster.Conf.Verbose, msg.Module, msg.Level, msg.Fields, msg.Text)
	}
}
