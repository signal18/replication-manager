package main

import (
	_ "database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/mariadb-tools/common"
	"github.com/tanji/mariadb-tools/dbhelper"
	"github.com/nsf/termbox-go"
	"os"
	"time"
)

var db *sqlx.DB
var version = flag.Bool("version", false, "Return version")
var user = flag.String("user", "", "User for MariaDB login")
var password = flag.String("password", "", "Password for MariaDB login")
var host = flag.String("host", "", "MariaDB host IP address or FQDN")
var socket = flag.String("socket", "/var/run/mysqld/mysqld.sock", "Path of MariaDB unix socket")
var port = flag.String("port", "3306", "TCP Port of MariaDB server")

func print_tb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func printf_tb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	print_tb(x, y, fg, bg, s)
}

func main() {

	flag.Parse()
	if *version == true {
		common.Version()
	}

	db = dbhelper.Connect(*user, *password, dbhelper.GetAddress(*host, *port, *socket))

	defer db.Close()
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	for {
		displayPl()
		go ifKeyPressed()
	}
}

func ifKeyPressed() {
	switch ev := termbox.PollEvent(); ev.Type {
	case termbox.EventKey:
		if ev.Key == termbox.KeyCtrlS {
			termbox.Sync()
		}
		if ev.Key == termbox.KeyCtrlQ {
			termbox.Close()
			db.Close()
			os.Exit(0)
		}
	}
}

func displayPl() {
	termbox.Clear(termbox.ColorWhite, termbox.ColorBlack)
	print_tb(0, 0, termbox.ColorWhite, termbox.ColorBlack, "MariaDB Processlist Monitor")
	plist := dbhelper.GetProcesslist(db)
	printf_tb(0, 2, termbox.ColorWhite|termbox.AttrBold, termbox.ColorBlack, "%8s %8s %10s %10s %20s %8s %20s", "Id", "User", "Host", "Database", "Command", "Time", "State")
	vy := 3
	for _, v := range plist {
		var database string
		if v.Database.Valid {
			database = v.Database.String
		} else {
			database = "NULL"
		}
		printf_tb(0, vy, termbox.ColorWhite, termbox.ColorBlack, "%8.8d %8.8s %10.10s %10.10v %20.20s %8.2f %20.20s", v.Id, v.User, v.Host, database, v.Command, v.Time*1000, v.State)
		vy++
	}
	termbox.Flush()
	time.Sleep(time.Duration(3) * time.Second)
}
