// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package misc

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
)

/* Returns two host and port items from a pair, e.g. host:port */
func SplitHostPort(s string) (string, string) {

	if strings.Count(s, ":") >= 2 {
		// IPV6
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return "", "3306"
		} else {
			return "[" + host + "]", port
		}
	} else {
		// not IPV6
		items := strings.Split(s, ":")
		if len(items) == 1 {
			return items[0], "3306"
		}
		return items[0], items[1]
	}

}

func SplitHostPortDB(s string) (string, string, string) {
	dbitems := strings.Split(s, "/")
	s = dbitems[0]
	host, port := SplitHostPort(s)
	if len(dbitems) > 1 {
		return host, port, dbitems[1]
	}
	return host, port, ""

}

/* Returns generic items from a pair, e.g. user:pass */
func SplitPair(s string) (string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], ""
	}
	if len(items) > 2 {
		return items[0], strings.Join(items[1:], ":")
	}
	return items[0], items[1]
}

/* Validate server host and port */
func ValidateHostPort(h string, p string) bool {
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
	}
	return false
}

/* Get local host IP */
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Fatalln("Error getting local IP address")
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func GetIPSafe(h string) (string, error) {
	ips, err := net.LookupIP(h)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
		if ip.To16() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("Could not resolve host name %s to IP", h)
}

func Contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func ExtractKey(s string, r map[string]string) string {
	s2 := s
	matches := regexp.MustCompile(`\%%(.*?)\%%`).FindAllStringSubmatch(s, -1)

	if matches == nil {
		return s2
	}

	for _, match := range matches {
		s2 = strings.Replace(s2, match[0], r[match[0]], -1)
	}
	return s2
}

func Unbracket(mystring string) string {
	return strings.Replace(strings.Replace(mystring, "[", "", -1), "]", "", -1)
}
