// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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
	"net/url"
	"regexp"
	"slices"
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

/* Returns two host and port items from a pair, e.g. host:port */
func SplitHostPortApp(s string) (string, string) {

	if strings.Count(s, ":") >= 2 {
		// IPV6
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return "", "80"
		} else {
			return "[" + host + "]", port
		}
	} else {
		// not IPV6
		items := strings.Split(s, ":")
		if len(items) == 1 {
			return items[0], "80"
		}
		return items[0], items[1]
	}

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

func SplitAcls(s string) (string, string, string, string) {
	items := strings.Split(s, ":")
	if len(items) == 1 {
		return items[0], "", "", ""
	}
	if len(items) == 2 {
		return items[0], items[1], "", ""
	}
	if len(items) == 3 {
		return items[0], items[1], items[2], ""
	}
	if len(items) > 4 {
		return items[0], items[1], items[2], strings.Join(items[3:], ":")
	}
	return items[0], items[1], items[2], items[3]
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

func Bool2Int(b bool) int {
	if b {
		return 1
	}
	return 0
}

func RemoveEmptyString(slice []string) []string {
	// Create a new slice to hold the non-empty strings
	var result []string
	for _, str := range slice {
		if str != "" {
			result = append(result, str)
		}
	}
	return result
}

func SortKeysAsc(keys []string) []string {
	// Sort them so it will not push if no changes are made
	slices.SortStableFunc(keys, func(a, b string) int {
		if a < b {
			return -1
		} else if a > b {
			return 1
		} else {
			return 0
		}
	})

	return keys
}

func SortKeysDesc(keys []string) []string {
	// Sort them so it will not push if no changes are made
	slices.SortStableFunc(keys, func(a, b string) int {
		if a > b {
			return -1
		} else if a < b {
			return 1
		} else {
			return 0
		}
	})
	return keys
}

func GenerateRegex(whitelist, exclude []string) (*regexp.Regexp, error) {
	if whitelist == nil {
		whitelist = []string{}
	}
	if exclude == nil {
		exclude = []string{}
	}

	// Build whitelist pattern
	whitelistPattern := ".*" // Default: match anything
	if len(whitelist) > 0 {
		whitelistPattern = strings.Join(ConvertWildcards(whitelist), "|")
	}

	// Build final regex pattern
	var pattern string
	if len(exclude) > 0 {
		excludePattern := strings.Join(ConvertWildcards(exclude), "|")
		pattern = fmt.Sprintf(`^(?:%s)(?:(?!%s).)*$`, whitelistPattern, excludePattern)
	} else {
		pattern = fmt.Sprintf(`^(?:%s)$`, whitelistPattern)
	}

	// Compile and return the regex
	return regexp.Compile(pattern)
}

// ConvertWildcards converts patterns with "*" into regex-compatible ".*"
// Appends "$" to patterns without wildcards for exact matching.
// Escapes special regex characters, including `.` and `_`.
func ConvertWildcards(patterns []string) []string {
	converted := make([]string, len(patterns))
	for i, pattern := range patterns {
		escaped := regexp.QuoteMeta(pattern) // Escape special characters
		if strings.Contains(pattern, "*") {
			// Replace "\*" with ".*" for wildcard support
			converted[i] = strings.ReplaceAll(escaped, `\*`, `.*`)
		} else {
			// Append "$" for exact matches
			converted[i] = escaped + "$"
		}
	}
	return converted
}

// isValidDomainOrIP checks if the input is a valid domain or IP address and not "localhost"
func IsValidPublicDomainOrIP(input string) bool {
	if input == "localhost" {
		return false
	}

	// Check if it's a valid IP address
	ipv4Regex := `^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$`
	ipv6Regex := `^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$`
	ipv4, _ := regexp.MatchString(ipv4Regex, input)
	ipv6, _ := regexp.MatchString(ipv6Regex, input)

	if ipv4 || ipv6 {
		// Check if it's a valid IP address
		ip := net.ParseIP(input)
		if ip == nil {
			return false
		}

		// Check if it's a localhost IP
		if ip.IsLoopback() {
			return false
		}
	}
	domainRegex := `^[a-z]([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(:[0-9]{1,5})?$`
	matched, _ := regexp.MatchString(domainRegex, input)
	return matched
}

// isValidURL checks if the input is a valid URL with a valid domain or IP address
func IsValidPublicURL(input string) bool {
	parsedURL, err := url.Parse(input)
	if err != nil {
		return false
	}

	host := parsedURL.Hostname()
	return IsValidPublicDomainOrIP(host)
}

func RemoveFromList(list string, element string) string {
	// Split the list into a slice
	items := strings.Split(list, ",")

	// Create a new slice to hold the filtered items
	var filteredItems []string

	// Iterate over the items and add them to the filtered slice if they are not equal to the element
	for _, item := range items {
		if item != element {
			filteredItems = append(filteredItems, item)
		}
	}

	// Join the filtered items back into a comma-separated string
	return strings.Join(filteredItems, ",")
}
