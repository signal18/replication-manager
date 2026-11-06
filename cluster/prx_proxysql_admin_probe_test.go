package cluster

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// fakeProxySQLAdmin answers the MySQL handshake like ProxySQL's admin interface: refuse maps a
// login to the error code it is refused with, any other login is accepted.
func fakeProxySQLAdmin(t *testing.T, refuse map[string]uint16) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	write := func(c net.Conn, seq byte, payload []byte) {
		c.Write(append([]byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), seq}, payload...))
	}
	read := func(c net.Conn) (byte, []byte, error) {
		h := make([]byte, 4)
		if _, err := io.ReadFull(c, h); err != nil {
			return 0, nil, err
		}
		p := make([]byte, int(h[0])|int(h[1])<<8|int(h[2])<<16)
		_, err := io.ReadFull(c, p)
		return h[3], p, err
	}
	ok := []byte{0, 0, 0, 2, 0, 0, 0}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hs := []byte{10}
				hs = append(hs, "5.5.30\x00"...)
				hs = append(hs, 1, 0, 0, 0)
				hs = append(hs, "abcdefgh"...)
				hs = append(hs, 0, 0x00, 0x82, 33, 2, 0, 0x08, 0, 21)
				hs = append(hs, make([]byte, 10)...)
				hs = append(hs, "ijklmnopqrst\x00mysql_native_password\x00"...)
				write(c, 0, hs)
				seq, resp, err := read(c)
				if err != nil || len(resp) < 33 {
					return
				}
				user := string(resp[32:])
				for i, b := range user {
					if b == 0 {
						user = user[:i]
						break
					}
				}
				if code, refused := refuse[user]; refused {
					e := []byte{0xff, 0, 0}
					binary.LittleEndian.PutUint16(e[1:], code)
					e = append(e, "#HY000User '"+user+"' refused"...)
					write(c, seq+1, e)
					return
				}
				write(c, seq+1, ok)
				for {
					seq, cmd, err := read(c) // COM_QUERY of the driver's max_allowed_packet, COM_PING, COM_QUIT
					if err != nil {
						return
					}
					if len(cmd) > 0 && cmd[0] == 3 {
						col := []byte{3, 'd', 'e', 'f', 0, 0, 0, 1, 'x', 1, 'x', 0x0c, 33, 0, 20, 0, 0, 0, 0xfd, 0, 0, 0, 0, 0}
						eof := []byte{0xfe, 0, 0, 2, 0}
						write(c, seq+1, []byte{1})
						write(c, seq+2, col)
						write(c, seq+3, eof)
						write(c, seq+4, append([]byte{8}, "16777216"...))
						write(c, seq+5, eof)
						continue
					}
					write(c, seq+1, ok)
				}
			}(c)
		}
	}()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func newProbeProxy(t *testing.T, user string, refuse map[string]uint16) (*Cluster, *ProxySQLProxy) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.ProxysqlUser = user
	cl.Conf.Secrets.Set("proxysql-user", config.Secret{Value: user})
	cl.Conf.Secrets.Set("proxysql-password", config.Secret{Value: "pw"})
	prx := NewProxySQLProxy(0, cl, "127.0.0.1")
	prx.ClusterGroup = cl
	prx.Port = fakeProxySQLAdmin(t, refuse)
	return cl, prx
}

// ProxySQL refuses a remote admin login with 1040: replication-manager probes external with the
// same password and, when that connects, keeps it as the login, across a restart too.
func TestProxysqlConnectSwitchesToExternalOn1040(t *testing.T) {
	cl, prx := newProbeProxy(t, "admin", map[string]uint16{"admin": 1040})
	psql, err := prx.Connect()
	if err != nil {
		t.Fatalf("connect: %s", err)
	}
	psql.Connection.Close()
	if prx.User != "external" {
		t.Fatalf("the proxy logs in as %q, want external", prx.User)
	}
	if got := cl.GetProxysqlUser(); got != "external" {
		t.Fatalf("proxysql-user %q, want external", got)
	}
	if got := cl.Conf.Secrets.Get("proxysql-password").Value; got != "pw" {
		t.Fatalf("the password changed to %q", got)
	}
	if prx.Pass != "pw" {
		t.Fatalf("the proxy password changed to %q", prx.Pass)
	}
}

// When the probe is refused too, the login stays admin and the error is admin's.
func TestProxysqlConnectKeepsAdminWhenExternalIsRefused(t *testing.T) {
	cl, prx := newProbeProxy(t, "admin", map[string]uint16{"admin": 1040, "external": 1045})
	if _, err := prx.Connect(); err == nil {
		t.Fatal("connect succeeded, want the admin error")
	}
	if prx.User != "admin" || cl.GetProxysqlUser() != "admin" {
		t.Fatalf("login changed to %q / %q, want admin", prx.User, cl.GetProxysqlUser())
	}
}

// Only 1040 probes: another refusal of admin, or a login set on purpose, is left as it is.
func TestProxysqlConnectProbesOnlyOn1040ForAdmin(t *testing.T) {
	for name, c := range map[string]struct {
		user   string
		refuse map[string]uint16
	}{
		"access denied":  {"admin", map[string]uint16{"admin": 1045}},
		"user set by us": {"radmin", map[string]uint16{"radmin": 1040}},
	} {
		t.Run(name, func(t *testing.T) {
			cl, prx := newProbeProxy(t, c.user, c.refuse)
			if _, err := prx.Connect(); err == nil {
				t.Fatal("connect succeeded, want the error")
			}
			if prx.User != c.user || cl.GetProxysqlUser() != c.user {
				t.Fatalf("login changed to %q / %q, want %s", prx.User, cl.GetProxysqlUser(), c.user)
			}
		})
	}
}
