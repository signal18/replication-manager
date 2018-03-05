// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
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
	port        int
}

type ProtectedSSTconnections struct {
	SSTconnections map[int]*SST
	sync.Mutex
}

var SSTs = ProtectedSSTconnections{SSTconnections: make(map[int]*SST)}

func (cluster *Cluster) SSTCloseReceiver(destinationPort int) {
	SSTs.SSTconnections[destinationPort].in.(net.Conn).Close()
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

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":0")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 120))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogPrintf(LvlInfo, "Listening for SST on port %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle()

	return strconv.Itoa(destinationPort), nil
}

func (sst *SST) tcp_con_handle() {

	var err error

	defer func() {
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogPrintf(LvlInfo, "SST connection end cleanup %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
		port := sst.listener.Addr().(*net.TCPAddr).Port
		sst.tcplistener.Close()
		sst.file.Close()
		sst.listener.Close()
		SSTs.Lock()
		delete(SSTs.SSTconnections, port)
		SSTs.Unlock()
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy()

	select {

	case <-chan_to_stdout:
		if sst.cluster.Conf.LogSST {
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

				if sst.cluster.Conf.LogSST {
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

func (cluster *Cluster) SSTRunSender(backupfile string, sv *ServerMonitor) {

	client, err := net.Dial("tcp", fmt.Sprintf("%s:%d", sv.IP, 4444))
	if err != nil {
		cluster.LogPrintf(LvlErr, "SST Reseed failed connection to port 4444 server %s %s ", sv.IP, err)
		return
	}
	defer client.Close()
	file, err := os.Open(backupfile)
	if err != nil {
		cluster.LogPrintf(LvlErr, "SST Reseed failed connection to port 4444 server %s %s ", sv.IP, err)
		return
	}
	/*fileInfo, err := file.Stat()
	if err != nil {
		fmt.Println(err)
		return
	}*/

	sendBuffer := make([]byte, 16384)
	fmt.Println("Start sending file!")
	for {
		_, err = file.Read(sendBuffer)
		if err == io.EOF {
			break
		}
		client.Write(sendBuffer)
	}
	cluster.LogPrintf(LvlErr, "File has been sent, closing connection!")

}
