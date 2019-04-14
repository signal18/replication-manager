// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"regexp"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
)

/*
  To ease the use of commonly needed filter conditions we allow simpler
  filter statements than straight HAproxy ACL conditions. For example:

	condition1 := "hdr_sub(user-agent) Android"  // Haproxy notation
	condition2 := "user-agent=Android"           // Vamp notation

	Furthermore, every statement has some conveniences built in like case
	insensitivity ("has cookie" is equivalent to "Has Cookie")

*/

const (
	UserAgent      string = "^[uU]ser[-.][aA]gent[ ]?([!])?=[ ]?([a-zA-Z0-9]+)$"
	Host           string = "^[hH]ost[ ]?([!])?=[ ]?([a-zA-Z0-9.]+)$"
	CookieContains string = "^[cC]ookie (.*) [Cc]ontains (.*)$"
	HasCookie      string = "^[Hh]as [Cc]ookie (.*)$"
	MissesCookie   string = "^[Mm]isses [Cc]ookie (.*)$"
	HeaderContains string = "^[Hheader] (.*) [Cc]ontains (.*)$"
	HasHeader      string = "^[Hh]as [Hh]eader (.*)$"
	MissesHeader   string = "^[Mm]isses [Hh]eader (.*)$"
)

var (
	rxUserAgent      = regexp.MustCompile(UserAgent)
	rxHost           = regexp.MustCompile(Host)
	rxCookieContains = regexp.MustCompile(CookieContains)
	rxHasCookie      = regexp.MustCompile(HasCookie)
	rxMissesCookie   = regexp.MustCompile(MissesCookie)
	rxHeaderContains = regexp.MustCompile(HeaderContains)
	rxHasHeader      = regexp.MustCompile(HasHeader)
	rxMissesHeader   = regexp.MustCompile(MissesHeader)
)

func parseFilterCondition(condition string) (string, bool) {

	if result := rxUserAgent.FindStringSubmatch(condition); result != nil {
		if strings.TrimSpace(result[1]) == "!" {
			return ("hdr_sub(user-agent) " + strings.TrimSpace(result[2])), true
		}
		return ("hdr_sub(user-agent) " + strings.TrimSpace(result[2])), false

	}

	if result := rxHost.FindStringSubmatch(condition); result != nil {
		if strings.TrimSpace(result[1]) == "!" {
			return ("hdr_str(host) " + strings.TrimSpace(result[2])), true
		}
		return ("hdr_str(host) " + strings.TrimSpace(result[2])), false
	}

	if result := rxCookieContains.FindStringSubmatch(condition); result != nil {
		return ("cook_sub(" + strings.TrimSpace(result[1]) + ") " + strings.TrimSpace(result[2])), false
	}

	if result := rxHasCookie.FindStringSubmatch(condition); result != nil {
		return ("cook(" + strings.TrimSpace(result[1]) + ") -m found"), false
	}

	if result := rxMissesCookie.FindStringSubmatch(condition); result != nil {
		return ("cook_cnt(" + strings.TrimSpace(result[1]) + ") eq 0"), false
	}

	if result := rxHeaderContains.FindStringSubmatch(condition); result != nil {
		return ("hdr_sub(" + strings.TrimSpace(result[1]) + ") " + strings.TrimSpace(result[2])), false
	}

	if result := rxHasHeader.FindStringSubmatch(condition); result != nil {
		return ("hdr_cnt(" + strings.TrimSpace(result[1]) + ") gt 0"), false
	}

	if result := rxMissesHeader.FindStringSubmatch(condition); result != nil {
		return ("hdr_cnt(" + strings.TrimSpace(result[1]) + ") eq 0"), false
	}

	return condition, false
}

/*
a convenience function for:
1. Generating and/or checking the validity of filter names.
2. Setting the correct, full backend names in filters.
4. Parsing the filter condition to HAproxy ACL conditions
*/
func resolveFilters(route Route) ([]*Filter, *Error) {

	var resolvedFilters []*Filter

	for _, filter := range route.Filters {

		// if the filter name is not given, just generate one
		if len(filter.Name) == 0 {
			filter.Name = misc.GetUUID()
		}

		filter, err := parseFilter(route.Name, filter)

		if err != nil {
			return resolvedFilters, err
		}
		resolvedFilters = append(resolvedFilters, filter)
	}
	return resolvedFilters, nil
}

// check the filter for validity regarding ACL specs and calls the short code parser
func parseFilter(routeName string, filter *Filter) (*Filter, *Error) {

	if valid, err := Validate(filter); valid != true {
		return filter, &Error{400, err}
	}

	destination := FilterName(routeName, filter.Destination)
	acl := Filter{filter.Name, "", destination, false}

	acl.Condition, acl.Negate = parseFilterCondition(filter.Condition)
	return &acl, nil

}
