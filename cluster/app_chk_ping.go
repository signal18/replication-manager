// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"fmt"
	"net"
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

// pingHost resolves the host and sends one ICMP echo request, one deadline shared by the
// resolution, the send and the wait for the reply (the probe runs inline in the monitor
// tick: it must never block longer than timeout, F2/F3). It uses the unprivileged ICMP
// socket (SOCK_DGRAM, net.ipv4.ping_group_range) so the monitor needs no capability; when
// the kernel refuses that socket the error says so, and the app is NOT reported up on a
// probe that could not run. IPv4 only: the cluster networks are IPv4, a host with only an
// AAAA record is reported unreachable. On the unprivileged socket the kernel rewrites the
// echo identifier, so a reply is matched on its source address (and sequence), not on it.
func pingHost(host string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	var target net.IP
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
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
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	const seq = 1
	msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: 0, Seq: seq, Data: []byte("replication-manager app probe")}}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return fmt.Errorf("icmp marshal: %w", err)
	}
	if _, err := conn.WriteTo(wb, &net.UDPAddr{IP: target}); err != nil {
		return fmt.Errorf("icmp send to %s: %w", target, err)
	}
	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return fmt.Errorf("no echo reply from %s (%s) within %s: %w", host, target, timeout, err)
		}
		pa, ok := peer.(*net.UDPAddr)
		if !ok || !pa.IP.Equal(target) {
			continue // another host's reply on the shared socket
		}
		rm, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), rb[:n])
		if err != nil || rm.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if echo, ok := rm.Body.(*icmp.Echo); ok && echo.Seq != seq {
			continue
		}
		return nil
	}
}

// probeNoRoute runs the probe of an app without a route and returns the error key that
// a failure opens: APPERR009 for a ping, APPERR003 for a TCP connect to app-port.
func (app *App) probeNoRoute(timeout time.Duration) (string, error) {
	if appMonitorModeIsPing(app.AppConfig) {
		return ErrAppPingFailed, pingHost(app.GetHost(), timeout)
	}
	probe := config.Route{Name: "app-port", Protocol: "tcp", Port: app.AppConfig.AppPort}
	return ErrAppTCPConnectFailed, app.GetAppLocalTCPStatus(probe)
}

// noRouteProbeErrDesc renders the state text of a failed no-route probe.
func (app *App) noRouteProbeErrDesc(errKey string, err error) string {
	if errKey == ErrAppPingFailed {
		return fmt.Sprintf(config.ClusterError[ErrAppPingFailed], app.GetId(), app.GetHost(), err.Error())
	}
	return fmt.Sprintf(config.ClusterError[ErrAppTCPConnectFailed], app.GetId(), app.GetHost()+":"+app.AppConfig.AppPort+": "+err.Error())
}
