// common.go
package main

import (
	"log"
	"net"
	"strconv"
	"strings"
)

/* Returns two host and port items from a pair, e.g. host:port */
func splitHostPort(s string) (string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], "3306"
	} else {
		return items[0], items[1]
	}
}

/* Returns generic items from a pair, e.g. user:pass */
func splitPair(s string) (string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], ""
	} else {
		return items[0], items[1]
	}
}

/* Validate server host and port */
func validateHostPort(h string, p string) bool {
	if net.ParseIP(h) == nil {
		return false
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		/* Not an integer */
		return false
	}
	if port > 0 && port <= 65535 {
		return true
	} else {
		return false
	}
}

func getSeqFromGtid(gtid string) uint64 {
	e := strings.Split(gtid, "-")
	if len(e) != 3 {
		log.Fatalln("Error splitting GTID:", gtid)
	}
	s, err := strconv.ParseUint(e[2], 10, 64)
	if err != nil {
		log.Fatalln("Error getting sequence from GTID:", err)
	}
	return s
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
