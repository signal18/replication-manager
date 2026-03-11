// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains database connection helpers and address resolution utilities.
// It provides functions for establishing connections to MySQL, MariaDB, and PostgreSQL,
// as well as parsing and resolving database server addresses.

package dbhelper

import (
	"errors"
	"log"
	"net"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// MySQLConnect returns a MySQL/MariaDB database connection
// Parameters can include connection options like "timeout=5s"
func MySQLConnect(user string, password string, address string, parameters ...string) (*sqlx.DB, error) {
	dsn := user + ":" + password + "@" + address + "/"
	if len(parameters) > 0 {
		dsn += "?" + parameters[0]
	}
	db, err := sqlx.Connect("mysql", dsn)
	return db, err
}

// SQLiteConnect returns a SQLite connection for the arbitrator
func SQLiteConnect(path string) (*sqlx.DB, error) {
	db, err := sqlx.Connect("sqlite", path+"/arbitrator.db")
	return db, err
}

// GetAddress builds a database connection address from host, port, and socket
// Returns tcp(host:port) for network connections or unix(socket) for local socket
func GetAddress(host string, port string, socket string) string {
	var address string
	if host != "" {
		address = "tcp(" + host + ":" + port + ")"
	} else {
		address = "unix(" + socket + ")"
	}
	return address
}

// GetHostFromProcessList retrieves the host from the processlist for a specific user
// Deprecated: This method is not reliable as it depends on active connections
func GetHostFromProcessList(db *sqlx.DB, user string, version *version.Version) (string, string, error) {
	pl := []Processlist{}
	var err error
	logs := ""
	pl, logs, err = GetProcesslistTable(db, version, false, false, false, "50", user)
	if err != nil {
		return "N/A", logs, err
	}
	for i := range pl {
		if pl[i].User == user {
			return strings.Split(pl[i].Host, ":")[0], logs, err
		}
	}
	return "N/A", logs, err
}

// GetHostFromConnection gets the connected host from the current database connection
func GetHostFromConnection(db *sqlx.DB, user string, version *version.Version) (string, string, error) {
	if version == nil {
		return "N/A", "", errors.New("No database version")
	}
	var value string
	query := "select user()"
	if version.IsPostgreSQL() {
		query = "select inet_client_addr()"
	}
	err := db.QueryRowx(query).Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get SQL User()", err)
		return "N/A", query, err
	}
	if version.IsPostgreSQL() {
		return value, query, nil
	}
	return strings.Split(value, "@")[1], query, nil
}

// CheckHostAddr checks if a string is an IP address or hostname and returns an IP address
// If input is already an IP, returns it as-is. Otherwise performs DNS lookup.
func CheckHostAddr(h string) (string, error) {
	var err error
	if net.ParseIP(h) != nil {
		return h, err
	}
	ha, err := net.LookupHost(h)
	if err != nil {
		return "", err
	}
	return ha[0], err
}
