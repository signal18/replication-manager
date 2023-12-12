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
	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) SetMaster(host string, port string) (string, error) {

	if net.ParseIP(host) == nil {
		return r.SetMasterFQDN(host, port)
	}
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("set server service_write/leader addr " + host + " port " + port + "\n"))
	if err != nil {

		return "", err
	}

	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *Runtime) SetMasterFQDN(host string, port string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.Host+":"+r.Port, DefaultTimeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("set server service_write/leader fqdn " + host + " port " + port + "\n"))
	if err != nil {

		return "", err
	}

	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
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
	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
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
	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
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
	//	cluster.LogPrintf(LvlErr, "haproxy entering  readall stats: ")
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(result), nil
}
