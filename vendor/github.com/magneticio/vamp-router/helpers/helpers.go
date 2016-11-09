package helpers

import (
	"runtime"
)

const (
	MAC_HAPROXY_BIN_LOCATION   = "/usr/local/sbin/haproxy"
	LINUX_HAPROXY_BIN_LOCATION = "/usr/sbin/haproxy"
)

func HaproxyLocation() string {

	switch runtime.GOOS {
	case "darwin":
		return MAC_HAPROXY_BIN_LOCATION
	case "linux":
		return LINUX_HAPROXY_BIN_LOCATION
	}
	return LINUX_HAPROXY_BIN_LOCATION
}
