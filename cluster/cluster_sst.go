// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

type SST struct {
	in          io.Reader
	file        *os.File
	listener    net.Listener
	tcplistener *net.TCPListener
	out         io.Writer
	cluster     *Cluster
	sync.Mutex
}

var SSTconnections = make(map[int]*SST)

func (cluster *Cluster) SSTCloseReceiver(destinationPort int) {
	SSTconnections[destinationPort].in.(net.Conn).Close()
}

func (cluster *Cluster) SSTRunReceiver(filename string, openfile string) (string, error) {
	sst := new(SST)
	sst.cluster = cluster
	var writers []io.Writer

	var err error
	if openfile == ConstJobCreateFile {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	} else {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	}
	if err != nil {
		cluster.LogPrintf(LvlErr, "Open file failed for job %s %s", filename, err)
		return "", err
	}
	writers = append(writers, sst.file)

	sst.out = io.MultiWriter(writers...)

	sst.listener, err = net.Listen("tcp", cluster.conf.BindAddr+":0")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 120))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.conf.LogSST {
		cluster.LogPrintf(LvlInfo, "Listening for SST on port %d", destinationPort)
	}
	sst.Lock()
	SSTconnections[destinationPort] = sst
	sst.Unlock()
	sst.tcp_con_handle()

	return strconv.Itoa(destinationPort), nil
}

func (sst *SST) tcp_con_handle() {

	var err error

	defer func() {
		if sst.cluster.conf.LogSST {
			sst.cluster.LogPrintf(LvlInfo, "SST connection end cleanup %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}

		sst.tcplistener.Close()
		sst.file.Close()
		sst.listener.Close()
		sst.Lock()
		delete(SSTconnections, sst.listener.Addr().(*net.TCPAddr).Port)
		sst.Unlock()
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy()

	select {

	case <-chan_to_stdout:
		if sst.cluster.conf.LogSST {
			sst.cluster.LogPrintf(LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
	}
}

// Performs copy operation between streams: os and tcp streams
func (sst *SST) stream_copy() <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {

				if sst.cluster.conf.LogSST {
					sst.cluster.LogPrintf(LvlErr, "SST closing connection from stream_copy %v ", con.RemoteAddr())
				}
				sst.in.(net.Conn).Close()
			}
			sync_channel <- 0 // Notify that processing is finished
		}()
		for {
			var nBytes int
			var err error

			nBytes, err = sst.in.Read(buf)

			if err != nil {
				if err != io.EOF {
					sst.cluster.LogPrintf(LvlErr, "Read error: %s", err)
				}
				break
			}
			_, err = sst.out.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogPrintf(LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}
