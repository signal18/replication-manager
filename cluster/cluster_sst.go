// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"io"
	"log"
	"net"
	"os"
	"strconv"
)

var SSTconnections = make(map[string]net.Conn)

func (cluster *Cluster) SSTGetPort() string {
	startport := 4000
	return strconv.Itoa(startport + len(SSTconnections))
}

func (cluster *Cluster) SSTCloseReceiver(destinationPort string) {
	SSTconnections[destinationPort].Close()
}

func (cluster *Cluster) SSTRunReceiver(destinationPort string) {

	var writers []io.Writer
	filename := cluster.conf.WorkingDir + "/" + cluster.cfgGroup + "/xtrabackup.xbstream"
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Open file failed in backup physical %s", err)
	}
	writers = append(writers, file)
	defer file.Close()
	dest := io.MultiWriter(writers...)
	listener, err := net.Listen("tcp", "0.0.0.0:"+destinationPort)
	if err != nil {
		log.Fatalln(err)
	}
	cluster.LogPrintf(LvlInfo, "Listening for SST on port %s", destinationPort)
	con, err := listener.Accept()
	SSTconnections[destinationPort] = con
	cluster.tcp_con_handle(con, dest)

}

func (cluster *Cluster) tcp_con_handle(con net.Conn, out io.Writer) {

	chan_to_stdout := cluster.stream_copy(con, out)

	select {
	case <-chan_to_stdout:
		cluster.LogPrintf(LvlErr, "Remote connection is closed")

	}
}

// Performs copy operation between streams: os and tcp streams
func (cluster *Cluster) stream_copy(src io.Reader, dst io.Writer) <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := dst.(net.Conn); ok {
				con.Close()
				cluster.LogPrintf(LvlErr, "Connection from %v is closed", con.RemoteAddr())
			}
			sync_channel <- 0 // Notify that processing is finished
		}()
		for {
			var nBytes int
			var err error
			nBytes, err = src.Read(buf)
			if err != nil {
				if err != io.EOF {
					cluster.LogPrintf(LvlErr, "Read error: %s", err)
				}
				break
			}
			_, err = dst.Write(buf[0:nBytes])
			if err != nil {
				cluster.LogPrintf(LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}
