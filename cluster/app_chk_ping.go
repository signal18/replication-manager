// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// appMonitorModeIsPing: the app asked to be probed with an ICMP echo instead of a TCP
// connect to its port (#1919). Pure, for tests.
func appMonitorModeIsPing(cnf *config.AppConfig) bool {
	return cnf != nil && strings.EqualFold(strings.TrimSpace(cnf.AppMonitorMode), "ping")
}

// pingHost resolves the host and sends one ICMP echo request, waiting at most timeout
// for the reply. It uses the unprivileged ICMP socket (SOCK_DGRAM, net.ipv4.ping_group_range)
// so the monitor needs no capability; when the kernel refuses that socket the error says
// so, and the app is NOT reported up on a probe that could not run.
func pingHost(host string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	var target net.IP
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			target = v4
			break
		}
	}
	if target == nil {
		return fmt.Errorf("resolve %s: no IPv4 address", host)
	}
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return fmt.Errorf("icmp socket: %w (net.ipv4.ping_group_range must cover the monitor's group)", err)
	}
	defer conn.Close()
	id := os.Getpid() & 0xffff
	msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: 1, Data: []byte("replication-manager app probe")}}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return fmt.Errorf("icmp marshal: %w", err)
	}
	if _, err := conn.WriteTo(wb, &net.UDPAddr{IP: target}); err != nil {
		return fmt.Errorf("icmp send to %s: %w", target, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return fmt.Errorf("no echo reply from %s (%s) within %s: %w", host, target, timeout, err)
		}
		rm, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), rb[:n])
		if err != nil {
			continue
		}
		if rm.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if echo, ok := rm.Body.(*icmp.Echo); ok && echo.ID != id && echo.ID != 0 {
			// another process' reply on the shared unprivileged socket id space
			if pa, ok := peer.(*net.UDPAddr); ok && !pa.IP.Equal(target) {
				continue
			}
		}
		return nil
	}
}
