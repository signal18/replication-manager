// display.go
package main

import (
	"fmt"
	"io"
	"log"
	"time"

	"github.com/nsf/termbox-go"
)

func display() {
	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	headstr := fmt.Sprintf(" MariaDB Replication Monitor and Health Checker version %s ", repmgrVersion)
	if interactive == false {
		headstr += " |  Mode: Automatic "
	} else {
		headstr += " |  Mode: Interactive "
	}
	printfTb(0, 0, termbox.ColorWhite, termbox.ColorBlack|termbox.AttrReverse|termbox.AttrBold, headstr)
	printfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %15s %12s %20s %20s %30s %6s %3s", "Host", "Port", "Status", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	tlog.Line = 3
	for _, server := range servers {
		server.refresh()
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
		repHeal := server.healthCheck()
		printfTb(0, tlog.Line, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %15s %12s %20s %20s %30s %6d %3s", server.Host, server.Port, server.State, server.UsingGtid, gtidCurr, gtidSlave, repHeal, server.Delay.Int64, server.ReadOnly)
		tlog.Line++
	}
	tlog.Line++
	if master != nil {
		if master.State != stateFailed {
			printTb(0, tlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-S to switchover")
		} else {
			printTb(0, tlog.Line, termbox.ColorWhite, termbox.ColorBlack, " Ctrl-Q to quit, Ctrl-F to failover")
		}
	}
	tlog.Line = tlog.Line + 3
	tlog.Print()
	if !daemon {
		termbox.Flush()
		_, newlen := termbox.Size()
		if newlen == 0 {
			// pass
		} else if newlen > termlength {
			termlength = newlen
			tlog.Len = termlength - 9 - (len(hostList) * 3)
			tlog.Extend()
		} else if newlen < termlength {
			termlength = newlen
			tlog.Len = termlength - 9 - (len(hostList) * 3)
			tlog.Shrink()
		}
	}
}

func printTb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func printfTb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	printTb(x, y, fg, bg, s)
}

func logprint(msg ...interface{}) {
	stamp := fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
	if logfile != "" {
		s := fmt.Sprint(stamp, " ", fmt.Sprintln(msg...))
		io.WriteString(logPtr, fmt.Sprint(s))
	}
	if tlog.Len > 0 {
		tlog.Add(fmt.Sprintln(msg...))
	} else {
		log.Println(msg...)
	}
}

func logprintf(format string, args ...interface{}) {
	if logfile != "" {
		f := fmt.Sprintln(fmt.Sprint(time.Now().Format("2006/01/02 15:04:05")), format)
		io.WriteString(logPtr, fmt.Sprintf(f, args...))
	}
	if tlog.Len > 0 {
		tlog.Add(fmt.Sprintf(format, args...))
	} else {
		log.Printf(format, args...)
	}
}
