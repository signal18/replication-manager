// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// display.go
package cluster

import (
	"fmt"
	"io"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/nsf/termbox-go"
)

func (cluster *Cluster) display() {
	if cluster.cfgGroup != cluster.cfgGroupDisplay {
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

	if cluster.conf.LogFile != "" {
		s := fmt.Sprint(stamp, " [", cluster.cfgGroup, "] ", fmt.Sprint(msg...))
		io.WriteString(cluster.logPtr, fmt.Sprintln(s))
	}
	if cluster.tlog.Len > 0 {
		s := fmt.Sprint("[", cluster.cfgGroup, "] ", fmt.Sprint(msg...))
		cluster.tlog.Add(s)
		cluster.display()
	}
	if cluster.conf.Daemon {
		s := fmt.Sprint("[", cluster.cfgGroup, "] ", fmt.Sprint(msg...))
		log.Println(s)
	}
}

func (cluster *Cluster) LogPrintf(level string, format string, args ...interface{}) {
	padright := func(str, pad string, lenght int) string {
		for {
			str += pad
			if len(str) > lenght {
				return str[0:lenght]
			}
		}
	}
	cliformat := format
	format = "[" + cluster.cfgGroup + "] " + padright(level, " ", 5) + " - " + format
	if level == "DEBUG" && cluster.conf.LogLevel <= 1 {
		// Only print debug messages if loglevel > 1
	} else {
		if cluster.conf.LogFile != "" {
			f := fmt.Sprintln(fmt.Sprint(time.Now().Format("2006/01/02 15:04:05")), format)

			io.WriteString(cluster.logPtr, fmt.Sprintf(f, args...))
		}
		if cluster.tlog != nil && cluster.tlog.Len > 0 {
			cluster.tlog.Add(fmt.Sprintf(format, args...))
			cluster.display()
		}
	}
	if cluster.conf.Daemon {
		// wrap logrus levels
		switch level {
		case "ERROR":
			log.WithField("cluster", cluster.cfgGroup).Errorf(cliformat, args...)
		case "INFO":
			log.WithField("cluster", cluster.cfgGroup).Infof(cliformat, args...)
		case "DEBUG":
			log.WithField("cluster", cluster.cfgGroup).Debugf(cliformat, args...)
		case "WARN":
			log.WithField("cluster", cluster.cfgGroup).Warnf(cliformat, args...)
		case "TEST":
			log.WithFields(log.Fields{"cluster": cluster.cfgGroup, "type": "test"}).Infof(cliformat, args...)
		case "BENCH":
			log.WithFields(log.Fields{"cluster": cluster.cfgGroup, "type": "benchmark"}).Infof(cliformat, args...)
		case "ALERT":
			log.WithFields(log.Fields{"cluster": cluster.cfgGroup, "type": "alert"}).Warnf(cliformat, args...)
		case "STATE":
			log.WithFields(log.Fields{"cluster": cluster.cfgGroup, "type": "state", "err": cliformat[0:8]}).Warnf(cliformat[9:len(cliformat)], args...)
		default:
			log.Printf(cliformat, args...)
		}
	}
}
