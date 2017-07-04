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

	log "github.com/Sirupsen/logrus"

	"github.com/nsf/termbox-go"
)

func (cluster *Cluster) display() {
	if cluster.cfgGroup != cluster.cfgGroupDisplay {
		return
	}
	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" Replication Monitor and Health Checker for MariaDB and MySQL version %s ", cluster.repmgrVersion)
	if cluster.cfgGroup != "" {
		headstr += fmt.Sprintf("| Group: %s", cluster.cfgGroup)
	}
	if cluster.conf.Interactive == false {
		headstr += " |  Mode: Automatic "
	} else {
		headstr += " |  Mode: Manual "
	}
	cluster.printfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	cluster.printfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	cluster.tlog.Line = 3
	for _, server := range cluster.servers {
		// server.refresh()
		var gtidCurr string
		var gtidSlave string
		if server.CurrentGtid != nil {
			gtidCurr = server.CurrentGtid.Sprint()
		} else {
			gtidCurr = ""
		}
		if server.SlaveGtid != nil {
			gtidSlave = server.SlaveGtid.Sprint()
		} else {
			gtidSlave = ""
		}
		repHeal := server.replicationCheck()
		var fgCol termbox.Attribute
		switch server.State {
		case "Master":
			fgCol = termbox.ColorGreen
		case "Failed":
			fgCol = termbox.ColorRed
		case "Unconnected":
			fgCol = termbox.ColorBlue
		case "Suspect":
			fgCol = termbox.ColorMagenta
		case "SlaveErr":
			fgCol = termbox.ColorMagenta
		case "SlaveLate":
			fgCol = termbox.ColorYellow
		default:
			fgCol = termbox.ColorWhite
		}
		cluster.printfTb(0, cluster.tlog.Line, fgCol, termbox.ColorBlack, "%15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Host, server.Port, server.State, server.FailCount, server.UsingGtid, gtidCurr, gtidSlave, repHeal, server.Delay.Int64, server.ReadOnly)
		cluster.tlog.Line++
	}
	cluster.tlog.Line++
	if cluster.master != nil {
		if cluster.master.State != stateFailed {
			cluster.printTb(0, cluster.tlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-S to switchover")
		} else {
			cluster.printTb(0, cluster.tlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-F to failover, Ctrl-(N|P) to change Cluster,Ctrl-H to help")
		}
	}
	cluster.tlog.Line = cluster.tlog.Line + 3
	cluster.tlog.Print()
	if !cluster.conf.Daemon {
		termbox.Flush()
		_, newlen := termbox.Size()
		if newlen == 0 {
			// pass
		} else if newlen > cluster.termlength {
			cluster.termlength = newlen
			cluster.tlog.Len = cluster.termlength - 9 - (len(cluster.hostList) * 3)
			cluster.tlog.Extend()
		} else if newlen < cluster.termlength {
			cluster.termlength = newlen
			cluster.tlog.Len = cluster.termlength - 9 - (len(cluster.hostList) * 3)
			cluster.tlog.Shrink()
		}
	}
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
	if cluster.conf.LogFile != "" {
		f := fmt.Sprintln(fmt.Sprint(time.Now().Format("2006/01/02 15:04:05")), format)

		io.WriteString(cluster.logPtr, fmt.Sprintf(f, args...))
	}
	if cluster.tlog != nil && cluster.tlog.Len > 0 {
		cluster.tlog.Add(fmt.Sprintf(format, args...))
		cluster.display()
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
		default:
			log.Printf(cliformat, args...)
		}
	}
}
