package haproxy

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/signal18/replication-manager/utils/misc"
)

const DefaultTimeout = (1 * time.Second)

// ApiReadTimeout bounds how long ApiCmd waits for a response, so a wedged
// Runtime API socket can't block the caller indefinitely. WaitSrvRemovable
// uses its own, larger deadline.
const ApiReadTimeout = 5 * time.Second

func (r *Runtime) ApiCmd(cmd string) (string, error) {
	return r.ApiCmdWithTimeout(cmd, ApiReadTimeout)
}

// ApiCmdWithTimeout behaves like ApiCmd but lets the caller size the
// connection deadline explicitly, for commands (e.g. `wait`) whose expected
// duration exceeds ApiReadTimeout.
func (r *Runtime) ApiCmdWithTimeout(cmd string, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(misc.Unbracket(r.Host), r.Port), DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	_, err = conn.Write([]byte(cmd + "\n"))
	if err != nil {

		return "", err
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) GetVersion() (string, error) {
	return r.ApiCmd("show version ")
}

// SetMaster repoints the write backend's "leader" runtime slot at host:port.
// host may be an IP or an FQDN (dispatches to SetMasterFQDN accordingly).
// haproxy-mode=runtimeapi callers must pass a resolved IP (see
// cluster.ServerMonitor.RuntimeAPIAddr), not an FQDN: runtimeapi's static
// config no longer attaches a "resolvers" section to the "leader" slot, so
// SetMasterFQDN's runtime "fqdn" command would have nothing to resolve
// against.
func (r *Runtime) SetMaster(pool string, host string, port string) (string, error) {
	host = misc.Unbracket(host)
	if net.ParseIP(host) == nil {
		return r.SetMasterFQDN(pool, host, port)
	}
	return r.ApiCmd("set server " + pool + "/leader addr " + host + " port " + port)
}

// SetMasterFQDN repoints the write backend's "leader" runtime slot at an
// FQDN, for HAProxy configs using DNS-based server resolution.
func (r *Runtime) SetMasterFQDN(pool string, host string, port string) (string, error) {
	return r.ApiCmd("set server " + pool + "/leader fqdn " + host + " port " + port)
}

// SetReady clears a server's administrative maintenance/drain state.
func (r *Runtime) SetReady(name string, pool string) (string, error) {
	return r.ApiCmd("set server " + pool + "/" + name + " state ready")
}

// SetMaintenance puts a server into administrative maintenance.
func (r *Runtime) SetMaintenance(name string, pool string) (string, error) {
	return r.ApiCmd("set server " + pool + "/" + name + " state maint")
}

// SetDrain puts a server into administrative drain (structurally up for
// health checks, but not eligible for new connections).
func (r *Runtime) SetDrain(name string, pool string) (string, error) {
	return r.ApiCmd("set server " + pool + "/" + name + " state drain")
}

// AddServer instantiates a new dynamic server in an existing backend. The
// backend must use a balance algorithm that supports dynamic servers
// (roundrobin, leastconn, first, random). opts is appended verbatim (e.g.
// "check"). Requires HAProxy >= 2.6 (see haproxyMinVersionDynamicServers in
// cluster/prx_haproxy.go). The server starts in administrative MAINT
// regardless of "check" — see SetDrain/EnableHealth.
func (r *Runtime) AddServer(pool string, name string, host string, port string, opts string) (string, error) {
	cmd := "add server " + pool + "/" + name + " " + net.JoinHostPort(misc.Unbracket(host), port)
	if opts != "" {
		cmd += " " + opts
	}
	return r.ApiCmd(cmd)
}

// DelServer removes a server via the Runtime API — works on statically
// configured servers too, not just dynamically added ones, provided the
// server is drained and idle (enforced by HAProxy itself; see
// SetMaintenance and WaitSrvRemovable).
func (r *Runtime) DelServer(pool string, name string) (string, error) {
	return r.ApiCmd("del server " + pool + "/" + name)
}

// EnableHealth (re)activates health checks on a server.
func (r *Runtime) EnableHealth(pool string, name string) (string, error) {
	return r.ApiCmd("enable health " + pool + "/" + name)
}

// SetServerAddr changes an existing server's runtime address/port, on
// statically configured or dynamically added servers alike. host may be an
// IP or an FQDN, dispatching to "addr" or "fqdn" the same way
// SetMaster/SetMasterFQDN do. "fqdn" additionally requires a "resolvers"
// section in haproxy.cfg. haproxy-mode=runtimeapi callers must pass a
// resolved IP (see cluster.ServerMonitor.RuntimeAPIAddr), not an FQDN:
// runtimeapi's read-backend members never carry a "resolvers" clause.
func (r *Runtime) SetServerAddr(pool string, name string, host string, port string) (string, error) {
	host = misc.Unbracket(host)
	if net.ParseIP(host) == nil {
		return r.SetServerFQDN(pool, name, host, port)
	}
	return r.ApiCmd("set server " + pool + "/" + name + " addr " + host + " port " + port)
}

// SetServerFQDN changes an existing server's runtime FQDN/port. See
// SetServerAddr for the "resolvers" prerequisite this depends on.
func (r *Runtime) SetServerFQDN(pool string, name string, host string, port string) (string, error) {
	return r.ApiCmd("set server " + pool + "/" + name + " fqdn " + host + " port " + port)
}

// WaitSrvRemovable blocks, up to timeout, until a server has no more
// active/idle connections and can be safely removed with DelServer.
// Requires HAProxy >= 3.0.
func (r *Runtime) WaitSrvRemovable(pool string, name string, timeout time.Duration) (string, error) {
	cmd := fmt.Sprintf("wait %d srv-removable %s/%s", timeout.Milliseconds(), pool, name)
	return r.ApiCmdWithTimeout(cmd, timeout+ApiReadTimeout)
}
