// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"io"
	"net"

	"golang.org/x/crypto/ssh"
)

type Endpoint struct {
	Host string
	Port int
}

// Returns an endpoint as ip:port formatted string
func (endpoint *Endpoint) String() string {
	return fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port)
}

type SSHTunnel struct {
	Local  *Endpoint
	Server *Endpoint
	Remote *Endpoint

	Config *ssh.ClientConfig
}

func (tunnel *SSHTunnel) forward(localConn net.Conn, sshServerConn *ssh.Client) {
	remoteConn, err := sshServerConn.Dial("tcp", tunnel.Remote.String())
	if err != nil {
		return
	}

	copyConn := func(writer, reader net.Conn) {
		_, err := io.Copy(writer, reader)
		if err != nil {
		}
	}

	go copyConn(localConn, remoteConn)
	go copyConn(remoteConn, localConn)
}

// Start the tunnel
func (tunnel *SSHTunnel) Start() error {
	listener, err := net.Listen("tcp", tunnel.Local.String())
	if err != nil {
		return err
	}
	defer listener.Close()

	for {
		serverConn, err := ssh.Dial("tcp", tunnel.Server.String(), tunnel.Config)
		if err != nil {
			return err
		}
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go tunnel.forward(conn, serverConn)
	}
}

// Define the ssh tunnel using its endpoint and config data
func (cluster *Cluster) sshTunnelMonitor(rhost string, rport int, lport int, bhost string, buser string, bpass string) *SSHTunnel {
	localEndpoint := &Endpoint{
		Host: "localhost",
		Port: lport,
	}

	serverEndpoint := &Endpoint{
		Host: bhost,
		Port: 22,
	}

	remoteEndpoint := &Endpoint{
		Host: rhost,
		Port: rport,
	}

	sshConfig := &ssh.ClientConfig{
		User: buser,
		Auth: []ssh.AuthMethod{
			ssh.Password(bpass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return &SSHTunnel{
		Config: sshConfig,
		Local:  localEndpoint,
		Server: serverEndpoint,
		Remote: remoteEndpoint,
	}
}

func (cluster *Cluster) sshTunnelGetLocalPort() int {
	var port int
	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			panic(err)
		}

		listen, err := net.ListenTCP("tcp", addr)
		if err != nil {
			cluster.LogPrintf("ERROR", "Can't get tunnel port %s", err)
			continue
		}

		port = listen.Addr().(*net.TCPAddr).Port
		listen.Close()
		break
	}

	return port
}
