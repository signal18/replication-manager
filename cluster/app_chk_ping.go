// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// appMonitorModeIsPing: the app asked to be probed with an ICMP echo instead of a TCP
// connect to its port (#1919). Pure, for tests.
func appMonitorModeIsPing(cnf *config.AppConfig) bool {
	return cnf != nil && strings.EqualFold(strings.TrimSpace(cnf.AppMonitorMode), "ping")
}

// pingHost resolves the host and sends one ICMP echo request, one deadline shared by the
// resolution, the send and the wait for the reply (the probe runs inline in the monitor
// tick: it must never block longer than timeout, F2/F3). An IPv4 address is preferred when
// the name has one, else the IPv6 address is pinged over ICMPv6: the product runs IPv6
// everywhere. The unprivileged ICMP sockets (SOCK_DGRAM, net.ipv4.ping_group_range, the
// same range applies to ICMPv6) need no capability; when the kernel refuses the socket the
// error says so, and the app is NOT reported up on a probe that could not run. On these
// sockets the kernel rewrites the echo identifier, so a reply is matched on its source
// address and sequence number, never on the identifier.
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
		for _, a := range addrs {
			if a.IP.To4() == nil && a.IP.To16() != nil {
				target = a.IP
				break
			}
		}
	}
	if target == nil {
		return fmt.Errorf("resolve %s: no address", host)
	}
	network, listen := "udp4", "0.0.0.0"
	echoType, replyType, proto := icmp.Type(ipv4.ICMPTypeEcho), icmp.Type(ipv4.ICMPTypeEchoReply), ipv4.ICMPTypeEchoReply.Protocol()
	if target.To4() == nil {
		network, listen = "udp6", "::"
		echoType, replyType, proto = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply.Protocol()
	}
	conn, err := icmp.ListenPacket(network, listen)
	if err != nil {
		return fmt.Errorf("icmp socket (%s): %w (net.ipv4.ping_group_range must cover the monitor's group)", network, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	const seq = 1
	msg := icmp.Message{Type: echoType, Code: 0, Body: &icmp.Echo{ID: 0, Seq: seq, Data: []byte("replication-manager app probe")}}
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
		rm, err := icmp.ParseMessage(proto, rb[:n])
		if err != nil || rm.Type != replyType {
			continue
		}
		echo, ok := rm.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq {
			continue // a reply without an echo body, or another probe's sequence
		}
		return nil
	}
}

// appMonitorHTTPRoute: the check of an app in app-monitor-mode http or https (#2209), as
// the route the HTTP check of the routes takes, never published on a gateway. A full
// app-host name (with a dot: a web service that lives elsewhere) is called as is, a short
// one on the app's cluster address. ok is false in the other modes. Pure, for tests.
func appMonitorHTTPRoute(cnf *config.AppConfig, clusterHost string) (route config.Route, ok bool) {
	if cnf == nil {
		return config.Route{}, false
	}
	mode := strings.ToLower(strings.TrimSpace(cnf.AppMonitorMode))
	switch mode {
	case "http", "https":
	case "https-cert":
		mode = "https" // the same GET, the certificate is checked next (checkCertificate)
	default:
		return config.Route{}, false
	}
	host := clusterHost
	if strings.Contains(cnf.AppHost, ".") {
		host = cnf.AppHost
	}
	monitor := &config.RouteMonitor{Path: cnf.AppMonitorPath, ExpectStatus: cnf.AppMonitorExpectStatus}
	monitor.Normalize()
	return config.Route{Name: "app-monitor", Protocol: mode, CName: host + ":" + cnf.AppPort, Monitor: monitor}, true
}

// appMonitorCertDefaultDays: app-monitor-cert-expire-days when unset.
const appMonitorCertDefaultDays = 14

// certExpiryError: nil while the certificate is valid for more than days, else what is
// wrong (expired, expires in N days). Pure, for tests.
func certExpiryError(notAfter, now time.Time, days int) error {
	if days <= 0 {
		days = appMonitorCertDefaultDays
	}
	left := notAfter.Sub(now)
	if left <= 0 {
		return fmt.Errorf("certificate expired on %s", notAfter.UTC().Format("2006-01-02"))
	}
	if left < time.Duration(days)*24*time.Hour {
		return fmt.Errorf("certificate expires in %d days, on %s (warning below %d days)", int(left.Hours()/24), notAfter.UTC().Format("2006-01-02"), days)
	}
	return nil
}

// checkCertificate opens a TLS connection to hostPort within timeout, verifies the
// server certificate (system roots, the host name) and its expiry (app-monitor-mode
// https-cert, #2209). The GET of the check runs without verification like the route
// checks; this is the separate, verified look at the certificate.
func checkCertificate(hostPort string, timeout time.Duration, days int) error {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return err
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: &tls.Config{ServerName: host}}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return fmt.Errorf("certificate of %s not valid: %w", hostPort, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("no certificate presented by %s", hostPort)
	}
	return certExpiryError(certs[0].NotAfter, time.Now(), days)
}

// probeNoRoute runs the probe of an app without a route and returns the error key that
// a failure opens: APPERR009 for a ping, APPERR001 or APPERR002 for an http/https check
// (no answer, unexpected status), APPERR010 for an https-cert check whose certificate is
// invalid or expiring, APPERR003 for a TCP connect to app-port.
func (app *App) probeNoRoute(timeout time.Duration) (string, error) {
	if appMonitorModeIsPing(app.AppConfig) {
		return ErrAppPingFailed, pingHost(app.GetHost(), timeout)
	}
	if route, ok := appMonitorHTTPRoute(app.AppConfig, app.GetHost()); ok {
		_, _, err := app.GetAppHTTPStatus(route, false)
		if isAppUnexpectedStatus(err) {
			return ErrAppUnexpectedStatus, err
		}
		if err != nil {
			return ErrAppConnectFailed, err
		}
		if appMonitorModeIsHTTPSCert(app.AppConfig) {
			return ErrAppCertificate, checkCertificate(route.CName, timeout, app.AppConfig.AppMonitorCertExpireDays)
		}
		return ErrAppConnectFailed, nil
	}
	probe := config.Route{Name: "app-port", Protocol: "tcp", Port: app.AppConfig.AppPort}
	return ErrAppTCPConnectFailed, app.GetAppLocalTCPStatus(probe)
}

// appMonitorModeIsHTTPSCert: the https check also verifies the certificate (#2209). Pure.
func appMonitorModeIsHTTPSCert(cnf *config.AppConfig) bool {
	return cnf != nil && strings.EqualFold(strings.TrimSpace(cnf.AppMonitorMode), "https-cert")
}

// noRouteProbeErrDesc renders the state text of a failed no-route probe.
func (app *App) noRouteProbeErrDesc(errKey string, err error) string {
	switch errKey {
	case ErrAppPingFailed:
		return fmt.Sprintf(config.ClusterError[ErrAppPingFailed], app.GetId(), app.GetHost(), err.Error())
	case ErrAppConnectFailed:
		return fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err.Error())
	case ErrAppUnexpectedStatus:
		var statusErr *appUnexpectedStatusError
		errors.As(err, &statusErr)
		code := 0
		if statusErr != nil {
			code = statusErr.Code
		}
		return fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), code)
	case ErrAppCertificate:
		return fmt.Sprintf(config.ClusterError[ErrAppCertificate], app.GetId(), err.Error())
	}
	return fmt.Sprintf(config.ClusterError[ErrAppTCPConnectFailed], app.GetId(), app.GetHost()+":"+app.AppConfig.AppPort+": "+err.Error())
}
