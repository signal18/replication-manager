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
	if master != nil {
		master.refresh()

		printfTb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %7s %20s %20s %12s", "Master Host", "Port", "Status", "Current GTID", "Binlog Position", "Strict Mode")
		printfTb(0, 3, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %7s %20s %20s %12s", master.Host, master.Port, master.State, master.CurrentGtid.Sprint(), master.BinlogPos.Sprint(), master.Strict)
		printfTb(0, 5, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %30s %6s %3s", "Slave Host", "Port", "Status", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")
	}
	tlog.Line = 6
	for _, slave := range slaves {

		slave.refresh()
		printfTb(0, tlog.Line, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %7s %12s %20s %20s %30s %6d %3s", slave.Host, slave.Port, slave.State, slave.UsingGtid, slave.CurrentGtid.Sprint(), slave.SlaveGtid.Sprint(), slave.healthCheck(), slave.Delay.Int64, slave.ReadOnly)
		tlog.Line++
	}
	tlog.Line++
	f := false
	for _, server := range servers {
		if server.State == stateUnconn || server.State == stateFailed {
			if f == false {
				printfTb(0, tlog.Line, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %41s %20s %12s", "Standalone Host", "Port", "Current GTID", "Binlog Position", "Strict Mode")
				f = true
				tlog.Line++
			}
			server.refresh()
			printfTb(0, tlog.Line, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%15s %6s %41s %20s %12s", "Master Host", "Port", "Current GTID", "Binlog Position", "Strict Mode")
			printfTb(0, tlog.Line, termbox.ColorWhite, termbox.ColorBlack, "%15s %6s %41s %20s %12s", server.Host, server.Port, server.CurrentGtid.Sprint(), server.BinlogPos.Sprint(), server.Strict)
			tlog.Line++
		}

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
		if newlen > termlength {
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
		display()
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
		display()
	} else {
		log.Printf(format, args...)
	}
}
