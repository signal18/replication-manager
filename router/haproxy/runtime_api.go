package haproxy

import (
	"io"
	"net"
	"time"
)

const DefaultTimeout = (1 * time.Second)

func (r *Runtime) ApiCmd(cmd string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(cmd + "\n"))
	if err != nil {

		return "", err
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) GetVersion() (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("show version \n"))
	if err != nil {

		return "", err
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) SetMaster(pool string, host string, port string) (string, error) {

	if net.ParseIP(host) == nil {
		return r.SetMasterFQDN(pool, host, port)
	}
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("set server " + pool + "/leader addr " + host + " port " + port + "\n"))
	if err != nil {

		return "", err
	}

	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) SetMasterFQDN(pool string, host string, port string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("set server " + pool + "/leader fqdn " + host + " port " + port + "\n"))
	if err != nil {

		return "", err
	}

	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) SetReady(name string, pool string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_, err = conn.Write([]byte("set server " + pool + "/" + name + " state ready\n"))
	if err != nil {
		return "", err
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

func (r *Runtime) SetMaintenance(name string, pool string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_, err = conn.Write([]byte("set server " + pool + "/" + name + " state maint\n"))
	if err != nil {
		return "", err
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

func (r *Runtime) SetDrain(name string, pool string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_, err = conn.Write([]byte("set server " + pool + "/" + name + " state drain\n"))
	if err != nil {
		return "", err
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// AddBackend creates a dynamic backend at runtime (HAProxy 3.4+, requires
// experimental-mode). Settings are inherited from the named defaults
// section, since the runtime API requires "from <defaults>" and rejects the
// command otherwise. The backend cannot receive traffic until PublishBackend
// is called on it.
func (r *Runtime) AddBackend(name string, mode string, fromDefaults string) (string, error) {
	return r.ApiCmd("experimental-mode on; add backend " + name + " from " + fromDefaults + " mode " + mode)
}

// PublishBackend makes a backend created with AddBackend available to
// receive traffic. Requires HAProxy 3.4+.
func (r *Runtime) PublishBackend(name string) (string, error) {
	return r.ApiCmd("publish backend " + name)
}

// AddServer adds a dynamic server to an existing backend. The backend must
// use a dynamic-compatible load-balancing algorithm (roundrobin, leastconn,
// first, random). The server starts in maintenance with health checks
// disabled; callers must follow up with EnableServer and EnableHealth.
// net.JoinHostPort brackets an IPv6 host (e.g. "[::1]:3307"), matching the
// bracketed form HAProxy's own "show stat"/"show servers state" report an
// IPv6 server's address in -- confirmed live against haproxy:3.4-alpine.
func (r *Runtime) AddServer(pool string, name string, host string, port string) (string, error) {
	return r.ApiCmd("add server " + pool + "/" + name + " " + net.JoinHostPort(host, port) + " check inter 1000 weight 100 maxconn 2000")
}

// EnableServer takes a dynamically added server out of maintenance mode.
func (r *Runtime) EnableServer(name string, pool string) (string, error) {
	return r.ApiCmd("enable server " + pool + "/" + name)
}

// EnableHealth resumes active health checks on a dynamically added server.
func (r *Runtime) EnableHealth(name string, pool string) (string, error) {
	return r.ApiCmd("enable health " + pool + "/" + name)
}

// DelServer removes a server, static or dynamic. The server must already be
// in maintenance with no active/idle connections; callers should call
// SetMaintenance before this. A rejection (e.g. the server doesn't exist, or
// still has connections) comes back as plain CLI text with err == nil, the
// same as every other command this package can't distinguish a rejection
// from a success on; see ShowServersState for how callers confirm which
// actually happened.
func (r *Runtime) DelServer(name string, pool string) (string, error) {
	return r.ApiCmd("del server " + pool + "/" + name)
}

// ShowServersState lists every server across every backend with its current
// state. Long-stable, non-experimental command, unlike "add backend"/
// "publish backend" (experimental, 3.4+), used to confirm a DelServer call
// actually removed the server rather than being rejected with CLI text
// ApiCmd can't see as an error.
func (r *Runtime) ShowServersState() (string, error) {
	return r.ApiCmd("show servers state")
}
