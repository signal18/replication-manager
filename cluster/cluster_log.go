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
	"time"

	"github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/utils/s18log"
	log "github.com/sirupsen/logrus"
	logsqlerr "github.com/sirupsen/logrus"
	logsqlgen "github.com/sirupsen/logrus"
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

func (cluster *Cluster) DisplayHelp() {
	cluster.LogPrint("HELP : Ctrl-D  Print debug information")
	cluster.LogPrint("HELP : Ctrl-F  Manual Failover")
	cluster.LogPrint("HELP : Ctrl-I  Toggle automatic/manual failover mode")
	cluster.LogPrint("HELP : Ctrl-R  Set slaves read-only")
	cluster.LogPrint("HELP : Ctrl-S  Switchover")
	cluster.LogPrint("HELP : Ctrl-N  Next Cluster")
	cluster.LogPrint("HELP : Ctrl-P  Previous Cluster")
	cluster.LogPrint("HELP : Ctrl-Q  Quit")
	cluster.LogPrint("HELP : Ctrl-C  Quit")
	cluster.LogPrint("HELP : Ctrl-W  Set slaves read-write")
}

func (cluster *Cluster) LogSQL(logs string, err error, url string, from string, level string, format string, args ...interface{}) {
	if err != nil {
		cluster.LogPrintf(level, format, args)

	}
	if logs != "" {
		if err != nil {
			cluster.LogSQLErrorPrintf(LvlInfo, url, err, from, logs, fmt.Sprintf(format, args...))
		}
		if from != "Monitor" {
			cluster.LogSQLGeneralPrintf(LvlInfo, url, from, logs)
		} else if cluster.Conf.LogSQLInMonitoring {
			cluster.LogSQLGeneralPrintf(LvlInfo, url, from, logs)
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

func (cluster *Cluster) LogSQLGeneralPrintf(level string, url string, from string, format string) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	msg := s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     level,
		Timestamp: stamp,
		Text:      format,
	}
	cluster.SQLGeneralLog.Add(msg)
	logsqlgen.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from}).Infof(format)
}

func (cluster *Cluster) LogSQLErrorPrintf(level string, url string, err error, from string, logs string, format string) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	msg := s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     level,
		Timestamp: stamp,
		Text:      logs,
	}
	cluster.SQLErrorLog.Add(msg)
	logsqlerr.WithFields(log.Fields{"cluster": cluster.Name, "server": url, "module": from, "error": err, "sql": logs}).Errorf(format)
}

func (cluster *Cluster) LogPrintf(level string, format string, args ...interface{}) {
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
	if level == "DEBUG" && cluster.Conf.LogLevel <= 1 {
		// Only print debug messages if loglevel > 1
	} else {
		if cluster.Conf.LogFile != "" {
			//			f := fmt.Sprintln(stamp, format)

			//	io.WriteString(cluster.logPtr, fmt.Sprintf(f, args...))
			//	log.WithField("cluster", cluster.Name).Debugf(cliformat, args...)
		}
		if cluster.tlog != nil && cluster.tlog.Len > 0 {
			cluster.tlog.Add(fmt.Sprintf(format, args...))
			cluster.display()
		}

		if cluster.Conf.HttpServ {
			msg := s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     level,
				Timestamp: stamp,
				Text:      fmt.Sprintf(cliformat, args...),
			}
			cluster.htlog.Add(msg)
			cluster.Log.Add(msg)
		}
	}

	if cluster.Conf.Daemon {
		// wrap logrus levels
		switch level {
		case "ERROR":
			log.WithField("cluster", cluster.Name).Errorf(cliformat, args...)
		case "INFO":
			log.WithField("cluster", cluster.Name).Infof(cliformat, args...)
		case "DEBUG":
			log.WithField("cluster", cluster.Name).Debugf(cliformat, args...)
		case "WARN":
			log.WithField("cluster", cluster.Name).Warnf(cliformat, args...)
		case "TEST":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "test"}).Infof(cliformat, args...)
		case "BENCH":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "benchmark"}).Infof(cliformat, args...)
		case "ALERT":
			log.WithFields(log.Fields{"cluster": cluster.Name, "type": "alert"}).Warnf(cliformat, args...)
		case "STATE":
			status := cliformat[0:6]
			code := cliformat[7:15]
			err := cliformat[18:]
			if status == "OPENED" {
				log.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": status, "code": code}).Warnf(err, args...)
			} else {
				log.WithFields(log.Fields{"cluster": cluster.Name, "type": "state", "status": status, "code": code}).Warnf(err, args...)
			}
		default:
			log.Printf(cliformat, args...)
		}
	}
}
