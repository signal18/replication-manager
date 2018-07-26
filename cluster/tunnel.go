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
	"log"
	"net"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/misc"
	"golang.org/x/crypto/ssh"
)

type tunnelSession struct {
	client     *ssh.Client
	listenAddr string
	remoteAddr string
}

func (cluster *Cluster) loginTunnel(cfg *config.Config) (*ssh.Client, error) {
	var methods []ssh.AuthMethod

	/*	if cfg.KeyPath != "" {
		key, err := ioutil.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read private key: %v", err)
		}


		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			log.Fatalf("unable to parse private key: %v", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	} */
	pwd, user := misc.SplitPair(cfg.TunnelCredential)
	if pwd == "" {
		return nil, fmt.Errorf("empty private key and password")
	}
	if pwd != "" {
		methods = append(methods, ssh.Password(pwd))
	}

	sshconfig := &ssh.ClientConfig{
		User: user,
		Auth: methods,
	}

	return ssh.Dial("tcp", cfg.TunnelHost, sshconfig)
}

func (cluster *Cluster) newTunnelSession(listen, remote string, client *ssh.Client) *tunnelSession {
	return &tunnelSession{
		client:     client,
		listenAddr: listen,
		remoteAddr: remote,
	}
}

func (s *tunnelSession) handleTunnelConn(conn net.Conn) {
	log.Printf("accept %s", conn.RemoteAddr())
	remote, err := s.client.Dial("tcp", s.remoteAddr)
	if err != nil {
		log.Printf("dial %s error", s.remoteAddr)
		return
	}
	log.Printf("%s -> %s connected.", conn.RemoteAddr(), s.remoteAddr)
	wait := new(sync.WaitGroup)
	wait.Add(2)
	go func() {
		io.Copy(remote, conn)
		remote.Close()
		wait.Done()
	}()
	go func() {
		io.Copy(conn, remote)
		conn.Close()
		wait.Done()
	}()
	wait.Wait()
	log.Printf("%s -> %s closed", conn.RemoteAddr(), s.remoteAddr)
}

func (s *tunnelSession) Run() error {
	l, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go s.handleTunnelConn(conn)
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
			cluster.LogPrintf(LvlErr, "Can't get tunnel port %s", err)
			continue
		}

		port = listen.Addr().(*net.TCPAddr).Port
		listen.Close()
		break
	}

	return port
}
