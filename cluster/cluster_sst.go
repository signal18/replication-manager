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
	"time"
)

type SST struct {
	in          io.Reader
	file        *os.File
	listener    net.Listener
	tcplistener *net.TCPListener
	out         io.Writer
	cluster     *Cluster
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
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0777)
	} else {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0777)
	}
	if err != nil {
		cluster.LogPrintf(LvlErr, "Open file failed for job %s %s", filename, err)
		return "", err
	}
	writers = append(writers, sst.file)

	sst.out = io.MultiWriter(writers...)

	sst.listener, err = net.Listen("tcp", "0.0.0.0:0")
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 120))
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	cluster.LogPrintf(LvlInfo, "Listening for SST on port %d", destinationPort)
	SSTconnections[destinationPort] = sst
	go sst.tcp_con_handle()

	return strconv.Itoa(destinationPort), nil
}

func (sst *SST) tcp_con_handle() {

	var err error

	defer func() {
		sst.cluster.LogPrintf(LvlInfo, "SST closed connection is closed %d", sst.listener.Addr().(*net.TCPAddr).Port)
		sst.file.Close()
		delete(SSTconnections, sst.listener.Addr().(*net.TCPAddr).Port)
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy()

	select {

	case <-chan_to_stdout:
		sst.cluster.LogPrintf(LvlErr, "Remote connection is closed ")

	}
	sst.cluster.LogPrintf(LvlErr, "after select ")
}

// Performs copy operation between streams: os and tcp streams
func (sst *SST) stream_copy() <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {
				con.Close()
				sst.cluster.LogPrintf(LvlErr, "Connection from %v is closed", con.RemoteAddr())
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
