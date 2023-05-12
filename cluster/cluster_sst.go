// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

type SST struct {
	in              io.Reader
	file            *os.File
	listener        net.Listener
	tcplistener     *net.TCPListener
	outfilewriter   io.Writer
	outresticreader io.WriteCloser
	cluster         *Cluster
	port            int
}

type ProtectedSSTconnections struct {
	SSTconnections map[int]*SST
	sync.Mutex
}

var SSTs = ProtectedSSTconnections{SSTconnections: make(map[int]*SST)}

func (cluster *Cluster) SSTCloseReceiver(destinationPort int) {
	SSTs.SSTconnections[destinationPort].in.(net.Conn).Close()
}

func (cluster *Cluster) SSTWatchRestic(r io.Reader) error {
	var out []byte
	buf := make([]byte, 1024, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			cluster.LogPrintf(LvlInfo, string(out))
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return err
		}
	}
}

func (cluster *Cluster) SSTRunReceiverToRestic(filename string) (string, error) {
	sst := new(SST)
	sst.cluster = cluster

	var err error

	resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, "backup", "--stdin", "--stdin-filename", filename)
	newEnv := append(os.Environ(), "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
	newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.BackupResticAwsAccessSecret)
	newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository)
	newEnv = append(newEnv, "RESTIC_PASSWORD="+cluster.Conf.BackupResticPassword)
	resticcmd.Env = newEnv

	stdout, err := resticcmd.StdoutPipe()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on restic StdoutPipe %s", err)
		return "", err
	}
	go cluster.SSTWatchRestic(stdout)
	sst.outresticreader, err = resticcmd.StdinPipe()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on restic StdinPipe %s", err)
		return "", err
	}
	err = resticcmd.Start()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Error restic command: %s", err)
		return "", err
	}

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
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
	go sst.tcp_con_handle_to_restic()

	return strconv.Itoa(destinationPort), nil
}

func (cluster *Cluster) SSTRunReceiverToFile(filename string, openfile string) (string, error) {
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

	sst.outfilewriter = io.MultiWriter(writers...)

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
	if err != nil {
		cluster.LogPrintf(LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 3600))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogPrintf(LvlInfo, "Listening for SST on port to file %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle_to_file()

	return strconv.Itoa(destinationPort), nil
}

func (sst *SST) tcp_con_handle_to_file() {

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
		sst.cluster.SSTSenderFreePort(strconv.Itoa(port))
		SSTs.Unlock()
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy_to_file()

	select {

	case <-chan_to_stdout:
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogPrintf(LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
	}
}

func (sst *SST) tcp_con_handle_to_restic() {

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
		sst.cluster.SSTSenderFreePort(strconv.Itoa(port))
		SSTs.Unlock()
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy_to_restic()

	select {

	case <-chan_to_stdout:
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogPrintf(LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
	}
}

// Performs copy operation between streams: os and tcp streams
func (sst *SST) stream_copy_to_file() <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {

				if sst.cluster.Conf.LogSST {
					sst.cluster.LogPrintf(LvlInfo, "SST closing connection from stream_copy %v ", con.RemoteAddr())
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
			_, err = sst.outfilewriter.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogPrintf(LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}

func (sst *SST) stream_copy_to_restic() <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {

				if sst.cluster.Conf.LogSST {
					sst.cluster.LogPrintf(LvlInfo, "SST closing connection from stream_copy %v ", con.RemoteAddr())
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
			_, err = sst.outresticreader.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogPrintf(LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}

func (cluster *Cluster) SSTRunSender(backupfile string, sv *ServerMonitor) {
	port, _ := strconv.Atoi(sv.SSTPort)

	if cluster.Conf.SchedulerReceiverUseSSL {
		cluster.SSTRunSenderSSL(backupfile, sv)
		return
	}
	client, err := net.Dial("tcp", fmt.Sprintf("%s:%d", sv.Host, port))
	if err != nil {
		cluster.LogPrintf(LvlErr, "SST Reseed failed connection to port %s server %s %s ", sv.SSTPort, sv.Host, err)
		return
	}

	defer client.Close()
	file, err := os.Open(backupfile)
	cluster.LogPrintf(LvlInfo, "SST sending file: %s to node: %s port: %s", backupfile, sv.Host, sv.SSTPort)
	if err != nil {
		cluster.LogPrintf(LvlErr, "SST failed to open backup file server %s %s ", sv.URL, err)
		return
	}

	sendBuffer := make([]byte, 16384)
	//fmt.Println("Start sending file!")
	var total uint64
	for {
		_, err = file.Read(sendBuffer)
		if err == io.EOF {
			break
		}
		bts, err := client.Write(sendBuffer)
		if err != nil {
			cluster.LogPrintf(LvlErr, "SST failed to write chunk %s at position %d", err, total)
		}
		total = total + uint64(bts)
	}
	cluster.LogPrintf(LvlInfo, "Backup has been sent, closing connection!")

}

func (cluster *Cluster) SSTRunSenderSSL(backupfile string, sv *ServerMonitor) {
	var (
		client *tls.Conn
		err    error
	)
	port, _ := strconv.Atoi(sv.SSTPort)

	config := &tls.Config{InsecureSkipVerify: true}
	if client, err = tls.Dial("tcp", fmt.Sprintf("%s:%d", sv.Host, port), config); err != nil {
		cluster.LogPrintf(LvlErr, "SST Reseed failed connection via SSL to port %s server %s %s ", sv.SSTPort, sv.Host, err)
		return
	}
	defer client.Close()
	file, err := os.Open(backupfile)
	cluster.LogPrintf(LvlInfo, "SST sending file via SSL: %s to node: %s port: %s", backupfile, sv.Host, sv.SSTPort)
	if err != nil {
		cluster.LogPrintf(LvlErr, "SST failed to open backup file server %s %s ", sv.URL, err)
		return
	}
	sendBuffer := make([]byte, 16384)
	var total uint64
	for {
		_, err = file.Read(sendBuffer)
		if err == io.EOF {
			break
		}
		bts, err := client.Write(sendBuffer)
		if err != nil {
			cluster.LogPrintf(LvlErr, "SST failed to write chunk %s at position %d", err, total)
		}
		total = total + uint64(bts)
	}
	cluster.LogPrintf(LvlInfo, "Backup has been sent via SSL , closing connection!")
}

func (cluster *Cluster) SSTGetSenderPort() string {
	port := "0"
	if cluster.Conf.SchedulerSenderPorts != "" {
		for k, v := range cluster.SstAvailablePorts {
			delete(cluster.SstAvailablePorts, k)
			return v
		}
	}
	return port
}

func (cluster *Cluster) SSTSenderFreePort(port string) {
	cluster.SstAvailablePorts[port] = port
}
