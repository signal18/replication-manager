// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"io"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"golang.org/x/crypto/ssh"
)

func (server *ServerMonitor) GetTunnelLocalPort() int {
	cluster := server.ClusterGroup
	var port int
	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			panic(err)
		}

		listen, err := net.ListenTCP("tcp", addr)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Can't get tunnel port %s", err)
			continue
		}

		port = listen.Addr().(*net.TCPAddr).Port
		listen.Close()
		break
	}

	return port
}

// Get private key for ssh authentication
func (server *ServerMonitor) parsePrivateKey(keyPath string) (ssh.Signer, error) {
	buff, _ := os.ReadFile(keyPath)
	return ssh.ParsePrivateKey(buff)
}

func (server *ServerMonitor) makeSshConfig(user, password string) (*ssh.ClientConfig, error) {

	key, err := server.parsePrivateKey(server.ClusterGroup.Conf.TunnelKeyPath)
	if err != nil {
		return nil, err
	}

	config := ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(key),
			ssh.Password(password),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}

	return &config, nil
}

// Handle local client connections and tunnel data to the remote serverq
// Will use io.Copy - http://golang.org/pkg/io/#Copy
func (server *ServerMonitor) handleTunnelClient(client net.Conn, remote net.Conn) {
	defer client.Close()
	chDone := make(chan bool)

	// Start remote -> local data transfer
	go func() {
		_, err := io.Copy(client, remote)
		if err != nil {
			log.Println("error while copy remote->local:", err)
		}
		chDone <- true
	}()

	// Start local -> remote data transfer
	go func() {
		_, err := io.Copy(remote, client)
		if err != nil {
			log.Println(err)
		}
		chDone <- true
	}()

	<-chDone
}

func (server *ServerMonitor) Tunnel() {
	cluster := server.ClusterGroup
	// Connection settings
	sshAddr := cluster.Conf.TunnelHost
	server.TunnelPort = strconv.Itoa(server.GetTunnelLocalPort())
	localAddr := "127.0.0.1:" + server.TunnelPort
	remoteAddr := server.Host + ":" + server.Port
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Opening tunnel from %s to %s", localAddr, remoteAddr)
	// Build SSH client configuration
	user, pwd := misc.SplitPair(server.ClusterGroup.Conf.TunnelCredential)
	cfg, err := server.makeSshConfig(user, pwd)
	if err != nil {
		log.Fatalln(err)
	}

	// Establish connection with SSH server
	conn, err := ssh.Dial("tcp", sshAddr, cfg)
	if err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	// Establish connection with remote server
	remote, err := conn.Dial("tcp", remoteAddr)
	if err != nil {
		log.Fatalln(err)
	}

	// Start local server to forward traffic to remote connection
	local, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Fatalln(err)
	}
	defer local.Close()

	// Handle incoming connections
	for {
		client, err := local.Accept()
		if err != nil {
			log.Fatalln(err)
		}

		server.handleTunnelClient(client, remote)
	}
}
