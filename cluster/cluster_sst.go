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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
)

// sstStreamIdleTimeout bounds how long stream_copy_to_file will wait for the
// next byte before giving up on a stalled/hung sender. A var, not a const,
// so tests can shrink it.
//
// Matches the existing 1-hour accept-deadline convention already used by
// both SSTRunReceiverToFile and SSTRunReceiverToDBLogFile: listener
// SetDeadline only bounds Accept() (see net.TCPListener.SetDeadline), not
// the connection it returns, so without a deadline of its own here a stuck
// sender blocks stream_copy_to_file's Read loop -- and anything holding the
// SST receiver's DB log writer borrow open (see getDBLogRotatingWriter,
// srv_job_logs.go) -- forever.
var sstStreamIdleTimeout = time.Hour

type SST struct {
	in                 io.Reader
	file               *os.File
	rotateWriter       io.WriteCloser
	listener           net.Listener
	tcplistener        *net.TCPListener
	outfilewriter      io.Writer
	outresticreader    io.WriteCloser
	outfilegzipwriter  *gzip.Writer
	cluster            *Cluster
	Filename           string // destination path, if this receiver writes to a file; used to detect an in-flight receiver for a given path (see IsFileOpenForSSTReceive)
	dbLogWriterRelease func() // set when outfilewriter is a shared ServerMonitor.getDBLogRotatingWriter borrow; must be called exactly once when this receiver is done writing (see tcp_con_handle_to_file), so a concurrent cache eviction doesn't close the writer out from under this still-streaming receiver
}

// IsFileOpenForSSTReceive reports whether filename currently has an active
// SST file receiver (scheduler-mode job or API-mode receive-task) writing to
// it. Used to avoid migrating a fetched DB log file while a receiver might
// still append to it -- see migrateDBLogsToBackupStorage in srv_job_logs.go.
func (cluster *Cluster) IsFileOpenForSSTReceive(filename string) bool {
	SSTs.Lock()
	defer SSTs.Unlock()
	for _, s := range SSTs.SSTconnections {
		if s.Filename == filename {
			return true
		}
	}
	return false
}

type SSTStreamOpener func() (io.ReadCloser, int64, error)

type ProtectedSSTconnections struct {
	SSTconnections map[int]*SST
	sync.Mutex
}

var SSTs = ProtectedSSTconnections{SSTconnections: make(map[int]*SST)}

func (cluster *Cluster) SSTCloseReceiver(destinationPort int) {
	sstRcvr := SSTs.SSTconnections[destinationPort]
	if sstRcvr != nil && sstRcvr.in != nil {
		sstRcvr.in.(net.Conn).Close()
	}
}

func (cluster *Cluster) SSTWatchRestic(r io.Reader) error {
	var out []byte
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, string(out))
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
	resticcmd.Env = cluster.ResticGetEnv()

	stdout, err := resticcmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on restic StdoutPipe %s", err)
		return "", err
	}
	go cluster.SSTWatchRestic(stdout)
	sst.outresticreader, err = resticcmd.StdinPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on restic StdinPipe %s", err)
		return "", err
	}
	err = resticcmd.Start()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Error restic command: %s", err)
		return "", err
	}

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 120))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Listening for SST on port %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle_to_restic()

	return strconv.Itoa(destinationPort), nil
}

func (cluster *Cluster) SSTRunReceiverToFile(server *ServerMonitor, filename string, openfile string, task string) (string, error) {
	sst := new(SST)
	sst.cluster = cluster
	sst.Filename = filename
	var writers []io.Writer

	var err error
	if openfile == ConstJobCreateFile {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	} else {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Open file failed for job %s %s", filename, err)
		return "", err
	}
	writers = append(writers, sst.file)

	sst.outfilewriter = io.MultiWriter(writers...)

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 3600))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Listening for SST on port to file %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle_to_file(server, task)

	return strconv.Itoa(destinationPort), nil
}

// SSTRunReceiverToDBLogFile opens a receiver for one fetched DB working-dir
// log (log_error.log, log_slow_query.log, log_sql_error.log, log_audit.log),
// identified by kind.
//
// When cluster.Conf.DBLogRotate is disabled (compatibility-first default),
// it behaves exactly like SSTRunReceiverToFile in append mode: repman does
// not rotate or prune the file, leaving retention to external logrotate.
//
// When enabled, writes go through this server's shared, long-lived rotating
// writer for kind (see ServerMonitor.getDBLogRotatingWriter) using DB-log-
// specific thresholds, independent of the generic log-rotate-* repman
// settings. That writer is server-owned and outlives this receiver, so it is
// intentionally not assigned to sst.rotateWriter -- only sst.outfilewriter --
// and the borrow is released (not closed) via sst.dbLogWriterRelease once the
// receiver's stream ends, in tcp_con_handle_to_file's cleanup.
func (cluster *Cluster) SSTRunReceiverToDBLogFile(server *ServerMonitor, kind DBLogKind, task string) (string, error) {
	filename := server.DBLogFilePath(kind)

	if !cluster.Conf.DBLogRotate {
		return cluster.SSTRunReceiverToFile(server, filename, ConstJobAppendFile, task)
	}

	sst := new(SST)
	sst.cluster = cluster
	sst.Filename = filename

	rw, release, err := server.getDBLogRotatingWriter(kind)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Open rotating writer failed for job %s %s", filename, err)
		return "", err
	}
	sst.outfilewriter = rw
	sst.dbLogWriterRelease = release

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
	if err != nil {
		release()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 3600))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Listening for SST on port to rotating file %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle_to_file(server, task)

	return strconv.Itoa(destinationPort), nil
}

func (cluster *Cluster) SSTRunReceiverToGZip(server *ServerMonitor, filename string, openfile string, task string) (string, error) {
	sst := new(SST)
	sst.cluster = cluster

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Compressing mariadb backup")

	var err error
	if openfile == ConstJobCreateFile {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	} else {
		sst.file, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Open file failed for job %s %s", filename, err)
		return "", err
	}

	// Use configurable compression level for better performance/size tradeoff
	compressionLevel := cluster.getSanitizedCompressionLevel(config.ConstLogModSST)
	gw, err := gzip.NewWriterLevel(sst.file, compressionLevel)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Error creating gzip writer: %s", err)
		return "", err
	}

	sst.outfilegzipwriter = gw

	sst.listener, err = net.Listen("tcp", cluster.Conf.BindAddr+":"+cluster.SSTGetSenderPort())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Exiting SST on socket listen %s", err)
		return "", err
	}
	sst.tcplistener = sst.listener.(*net.TCPListener)
	sst.tcplistener.SetDeadline(time.Now().Add(time.Second * 3600))
	destinationPort := sst.listener.Addr().(*net.TCPAddr).Port
	if sst.cluster.Conf.LogSST {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Listening for SST on port to file %d", destinationPort)
	}
	SSTs.Lock()
	SSTs.SSTconnections[destinationPort] = sst
	SSTs.Unlock()
	go sst.tcp_con_handle_to_gzip(server, task)

	return strconv.Itoa(destinationPort), nil
}

func (sst *SST) tcp_con_handle_to_gzip(server *ServerMonitor, task string) {

	var err error

	defer func() {
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST connection end cleanup %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
		port := sst.listener.Addr().(*net.TCPAddr).Port
		sst.tcplistener.Close()
		sst.outfilegzipwriter.Close()
		sst.file.Close()
		sst.listener.Close()
		SSTs.Lock()
		delete(SSTs.SSTconnections, port)
		sst.cluster.SSTSenderFreePort(strconv.Itoa(port))
		SSTs.Unlock()

		server.JobFinishReceiveFile(task)
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST connection error starting listener for task %s : %v", task, err)
		return
	}

	chan_to_stdout := sst.stream_copy_to_gzip()

	<-chan_to_stdout
	if sst.cluster.Conf.LogSST {
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
	}
}

func (sst *SST) tcp_con_handle_to_file(server *ServerMonitor, task string) {

	var err error

	defer func() {
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST connection end cleanup %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
		port := sst.listener.Addr().(*net.TCPAddr).Port
		sst.tcplistener.Close()
		sst.file.Close()
		if sst.rotateWriter != nil {
			sst.rotateWriter.Close()
		}
		if sst.dbLogWriterRelease != nil {
			sst.dbLogWriterRelease()
		}
		sst.listener.Close()
		SSTs.Lock()
		delete(SSTs.SSTconnections, port)
		sst.cluster.SSTSenderFreePort(strconv.Itoa(port))
		SSTs.Unlock()

		server.JobFinishReceiveFile(task)
	}()

	sst.in, err = sst.listener.Accept()
	if err != nil {
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST connection error starting listener for task %s: %v", task, err)
		return
	}

	chan_to_stdout := sst.stream_copy_to_file()

	<-chan_to_stdout
	if sst.cluster.Conf.LogSST {
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
	}
}

func (sst *SST) tcp_con_handle_to_restic() {

	var err error

	defer func() {
		if sst.cluster.Conf.LogSST {
			sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST connection end cleanup %d", sst.listener.Addr().(*net.TCPAddr).Port)
		}
		port := sst.listener.Addr().(*net.TCPAddr).Port
		sst.tcplistener.Close()
		sst.file.Close()
		sst.listener.Close()
		SSTs.Lock()
		delete(SSTs.SSTconnections, port)
		sst.cluster.SSTSenderFreePort(strconv.Itoa(port))
		SSTs.Unlock()
		sst.cluster.SetInPhysicalBackupState(false)
	}()

	sst.in, err = sst.listener.Accept()

	if err != nil {

		return
	}

	chan_to_stdout := sst.stream_copy_to_restic()

	<-chan_to_stdout
	if sst.cluster.Conf.LogSST {
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Chan SST out for %d", sst.listener.Addr().(*net.TCPAddr).Port)
	}
}

// Performs copy operation between streams: os and tcp streams
func (sst *SST) stream_copy_to_file() <-chan int {
	//coucou
	//buf := make([]byte, 1024)
	buf := make([]byte, 8192)

	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {

				if sst.cluster.Conf.LogSST {
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST closing connection from stream_copy %v ", con.RemoteAddr())
				}
				sst.in.(net.Conn).Close()
			}
			sync_channel <- 0 // Notify that processing is finished
		}()
		for {
			var nBytes int
			var err error

			if con, ok := sst.in.(net.Conn); ok {
				con.SetReadDeadline(time.Now().Add(sstStreamIdleTimeout))
			}
			nBytes, err = sst.in.Read(buf)

			if err != nil {
				if err != io.EOF {
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Read error: %s", err)
				}
				break
			}
			_, err = sst.outfilewriter.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}

func (sst *SST) stream_copy_to_gzip() <-chan int {
	//coucou
	//buf := make([]byte, 1024)
	buf := make([]byte, 8192)

	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := sst.in.(net.Conn); ok {

				if sst.cluster.Conf.LogSST {
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST closing connection from stream_copy %v ", con.RemoteAddr())
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
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Read error: %s", err)
				}
				break
			}

			_, err = sst.outfilegzipwriter.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Write error: %s", err)
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
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST closing connection from stream_copy %v ", con.RemoteAddr())
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
					sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Read error: %s", err)
				}
				break
			}
			_, err = sst.outresticreader.Write(buf[0:nBytes])
			if err != nil {
				sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Write error: %s", err)
			}
		}
	}()
	return sync_channel
}

// SSTProgressSink is a caller-supplied destination for an in-flight SST
// send's byte/total tracking. nil means "don't track" -- the SST send family
// (SSTRunSender and everything it calls) has zero knowledge of what a sink is
// ultimately backing; it only ever calls AddBytes/SetTotal on whatever it was
// handed. These functions are NOT reseed-only -- UpgradeJobsScript
// (srv_job.go) and the dummy-config sender (srv_cnf.go) also call
// SSTRunSender for unrelated transfers. Gating on an ambient signal like
// sv.IsReseeding wouldn't work: a reseed can legitimately be in progress on a
// server AT THE SAME TIME as one of those unrelated sends fires, so
// IsReseeding!="" would be true for both and couldn't tell them apart -- it
// would let the unrelated transfer overwrite the real reseed's counters
// mid-flight. Only the caller knows which case it is, so only the caller can
// decide: WaitAndSendSST/WaitAndSendSSTStream pass a sink backed by
// server.reseedBytes/reseedTotal; everyone else passes nil.
//
// A plain *atomic.Int64 for bytes alone isn't quite enough: the SST layer
// also decides *whether* a total is trustworthy (raw file send: yes, exact
// on-disk size; decompress-then-send: no, decompressed bytes sent never
// matches the compressed file size; stream send: only when not decompressing
// on the fly) -- that logic stays here, so the sink needs a place for both.
type SSTProgressSink struct {
	bytes *atomic.Int64
	total *atomic.Int64
}

// newReseedProgressSink builds the sink WaitAndSendSST/WaitAndSendSSTStream
// pass, backed directly by the server's reseed progress counters (the same
// fields ReseedProgressView/GetReseedProgress read).
func newReseedProgressSink(sv *ServerMonitor) *SSTProgressSink {
	return &SSTProgressSink{bytes: &sv.reseedBytes, total: &sv.reseedTotal}
}

// AddBytes records n more bytes sent. Safe to call on a nil sink (no-op).
func (s *SSTProgressSink) AddBytes(n int64) {
	if s == nil || s.bytes == nil {
		return
	}
	s.bytes.Add(n)
}

// SetTotal records the transfer's total size once the SST layer has decided
// it's trustworthy for this send path (see the type doc comment). Safe to
// call on a nil sink (no-op); total<=0 is treated as "still unknown" and
// left untouched rather than stored.
func (s *SSTProgressSink) SetTotal(total int64) {
	if s == nil || s.total == nil || total <= 0 {
		return
	}
	s.total.Store(total)
}

// sstProgressWriter wraps w so each successful Write's byte count reaches
// sink.AddBytes -- a no-op when sink is nil, so callers never need to branch
// on whether progress is being tracked; they just always wrap and the sink
// itself decides whether that's a real write or a discard.
type sstProgressWriter struct {
	w    io.Writer
	sink *SSTProgressSink
}

func (p *sstProgressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.sink.AddBytes(int64(n))
	return n, err
}

func (cluster *Cluster) SSTRunSender(backupfile string, sv *ServerMonitor, uncompress bool, progress *SSTProgressSink) error {
	var err error
	port, _ := strconv.Atoi(sv.SSTPort)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST Reseed to port %s server %s", sv.SSTPort, sv.Host)

	if cluster.Conf.SchedulerReceiverUseSSL {
		return cluster.SSTRunSenderSSL(backupfile, sv, progress)
	}

	client, err := net.Dial("tcp", net.JoinHostPort(sv.Host, fmt.Sprintf("%d", port)))
	if err != nil {
		return fmt.Errorf("SST Reseed failed connection to port %s server %s %s ", sv.SSTPort, sv.Host, err)
	}
	defer client.Close()

	if strings.HasSuffix(backupfile, "gz") && uncompress {
		err = cluster.SSTRunSendGzip(client, backupfile, sv, progress)
	} else {
		err = cluster.SSTRunSendFile(client, backupfile, sv, progress)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Backup failed to send, closing connection!")
		return fmt.Errorf("Error sending SST to server %s: %s ", sv.Host, err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Backup has been sent, closing connection!")
	}
	return nil
}

func (cluster *Cluster) SSTRunSenderStream(sourceName string, opener SSTStreamOpener, sv *ServerMonitor, uncompress bool, progress *SSTProgressSink) error {
	var err error
	port, _ := strconv.Atoi(sv.SSTPort)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST Reseed stream to port %s server %s", sv.SSTPort, sv.Host)

	if cluster.Conf.SchedulerReceiverUseSSL {
		return cluster.SSTRunSenderStreamSSL(sourceName, opener, sv, uncompress, progress)
	}

	client, err := net.Dial("tcp", net.JoinHostPort(sv.Host, fmt.Sprintf("%d", port)))
	if err != nil {
		return fmt.Errorf("SST Reseed stream failed connection to port %s server %s %s ", sv.SSTPort, sv.Host, err)
	}
	defer client.Close()

	if err = cluster.sstSendStream(client, sourceName, opener, sv, uncompress, progress); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Backup stream failed to send, closing connection!")
		return fmt.Errorf("Error sending SST stream to server %s: %s ", sv.Host, err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Backup stream has been sent, closing connection!")
	return nil
}

func (cluster *Cluster) SSTRunSenderStreamSSL(sourceName string, opener SSTStreamOpener, sv *ServerMonitor, uncompress bool, progress *SSTProgressSink) error {
	var (
		client *tls.Conn
		err    error
	)
	port, _ := strconv.Atoi(sv.SSTPort)

	tlsconfig := &tls.Config{InsecureSkipVerify: true}
	if client, err = tls.Dial("tcp", fmt.Sprintf("%s:%d", sv.Host, port), tlsconfig); err != nil {
		return fmt.Errorf("SST Reseed stream failed connection via SSL to port %s server %s %s ", sv.SSTPort, sv.Host, err)
	}
	defer client.Close()

	if err = cluster.sstSendStream(client, sourceName, opener, sv, uncompress, progress); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Backup stream failed to send via SSL, closing connection!")
		return fmt.Errorf("Error sending SST stream to server %s: %s ", sv.Host, err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Backup stream has been sent via SSL, closing connection!")
	return nil
}

func (cluster *Cluster) sstSendStream(client net.Conn, sourceName string, opener SSTStreamOpener, sv *ServerMonitor, uncompress bool, progress *SSTProgressSink) error {
	reader, expectedSize, err := opener()
	if err != nil {
		return fmt.Errorf("SST stream for %s failed to open source: %s", sourceName, err)
	}

	if reader == nil {
		return fmt.Errorf("SST stream failed: reader is nil")
	}

	if rc, ok := reader.(io.ReadCloser); ok {
		defer rc.Close()
	}

	streamReader := reader
	if uncompress && strings.HasSuffix(strings.ToLower(sourceName), ".gz") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream decompressing gzip source: %s", sourceName)
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return fmt.Errorf("SST stream failed to init gzip reader for %s: %w", sourceName, err)
		}
		defer gzReader.Close()
		streamReader = gzReader
	}

	bufSize := cluster.Conf.SSTSendBuffer
	if bufSize <= 0 {
		bufSize = 16384
	}
	buffer := make([]byte, bufSize)

	// expectedSize is only a trustworthy total when not decompressing on the
	// fly: with uncompress=true, bytes actually sent (decompressed) will never
	// match the source's expected/compressed size (see the mismatch check
	// below, which is deliberately skipped in that case for the same reason).
	// No-op when progress is nil.
	if !uncompress && expectedSize > 0 {
		progress.SetTotal(expectedSize)
	}
	dest := &sstProgressWriter{w: client, sink: progress}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST streaming source: %s to node: %s port: %s", sourceName, sv.Host, sv.SSTPort)
	bytesSent, err := io.CopyBuffer(dest, streamReader, buffer)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST stream failed to write: %s", err)
		return err
	}

	if expectedSize > 0 {
		if uncompress {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream completed for %s: sent=%d (source expected=%d)", sourceName, bytesSent, expectedSize)
		} else if int64(bytesSent) != expectedSize {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlWarn, "SST stream size mismatch for %s: sent=%d expected=%d", sourceName, bytesSent, expectedSize)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream completed for %s: sent=%d expected=%d", sourceName, bytesSent, expectedSize)
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream completed for %s: sent=%d", sourceName, bytesSent)
	}

	return nil
}

func (cluster *Cluster) SSTRunSendGzip(client net.Conn, backupfile string, sv *ServerMonitor, progress *SSTProgressSink) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST sending file: %s to node: %s port: %s", backupfile, sv.Host, sv.SSTPort)
	file, err := os.Open(backupfile)
	if err != nil {
		return fmt.Errorf("SST to server %s failed to open backup file, err: %s ", sv.URL, err)
	}

	sendBuffer := make([]byte, cluster.Conf.SSTSendBuffer)
	//fmt.Println("Start sending file!")
	var total uint64

	defer file.Close()

	// Total is never set on progress here: this path decompresses on the fly,
	// so bytes actually sent (decompressed) never matches the compressed
	// file's on-disk size -- see SSTRunSendFile for why a raw send can set a
	// trustworthy total but this can't.

	// Use configurable parallel blocks for better performance
	// For SST/reseed operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModSST)
	bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModSST)
	fz, err := gzip.NewReaderN(file, bufferSize, parallelBlocks)
	if err != nil {
		return fmt.Errorf("SST to server %s failed in init gzip reader, err: %s", sv.URL, err)
	}
	defer fz.Close()

	// Read and send data in chunks
	for {
		n, err := fz.Read(sendBuffer)
		if err != nil && err != io.EOF {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST failed to read decompressed data: %v", err)
		}
		if n > 0 {
			// Send the chunk to the network connection
			if bts, err := client.Write(sendBuffer[:n]); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST failed to write chunk at position %d: %v", total, err)
			} else {
				total = total + uint64(bts)
				progress.AddBytes(int64(bts))
			}
		}
		if err == io.EOF {
			break
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Backup has been sent, closing connection!")

	return nil
}

func (cluster *Cluster) SSTRunSendFile(client net.Conn, backupfile string, sv *ServerMonitor, progress *SSTProgressSink) error {
	file, err := os.Open(backupfile)
	if os.IsNotExist(err) && cluster.Conf.CompressBackups {
		backupfile = strings.Replace(backupfile, "xbtream", "gz", 1)
		return cluster.SSTRunSendGzip(client, backupfile, sv, progress)
	}
	if err != nil {
		return fmt.Errorf("SST to server %s failed to open backup file: %w", sv.URL, err)
	}
	defer file.Close()

	// Sent here means the file's bytes go over the wire unmodified (no
	// decompress-then-send, unlike SSTRunSendGzip), so the on-disk size is a
	// trustworthy total for the progress bar. No-op when progress is nil.
	if fi, err := file.Stat(); err == nil {
		progress.SetTotal(fi.Size())
	}

	bufSize := cluster.Conf.SSTSendBuffer
	readaheadDepth := 4 // number of chunks to prefetch
	ch := make(chan []byte, readaheadDepth)
	errCh := make(chan error, 1)
	done := make(chan struct{})

	// --- Reader goroutine (producer) ---
	go func() {
		defer close(ch)
		for {
			buf := make([]byte, bufSize)
			n, err := file.Read(buf)
			if err != nil {
				if err != io.EOF {
					errCh <- err
				}
				break
			}
			if n == 0 {
				break
			}
			ch <- buf[:n]
		}
	}()

	// --- Sender (consumer) ---
	var total uint64
	for {
		select {
		case buf, ok := <-ch:
			if !ok {
				// all done
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo,
					"Backup has been sent (%d bytes), closing connection!", total)
				close(done)
				return nil
			}
			n, err := client.Write(buf)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr,
					"SST failed to write chunk: %s at position %d", err, total)
				return err
			}
			total += uint64(n)
			progress.AddBytes(int64(n))

		case err := <-errCh:
			return fmt.Errorf("read error: %w", err)
		}
	}
}

func (cluster *Cluster) SSTRunSenderSSL(backupfile string, sv *ServerMonitor, progress *SSTProgressSink) error {
	var (
		client *tls.Conn
		err    error
	)
	port, _ := strconv.Atoi(sv.SSTPort)

	tlsconfig := &tls.Config{InsecureSkipVerify: true}
	if client, err = tls.Dial("tcp", fmt.Sprintf("%s:%d", sv.Host, port), tlsconfig); err != nil {
		return fmt.Errorf("SST Reseed failed connection via SSL to port %s server %s %s ", sv.SSTPort, sv.Host, err)
	}
	defer client.Close()

	if strings.HasSuffix(backupfile, "gz") {
		err = cluster.SSTRunSendGzip(client, backupfile, sv, progress)
	} else {
		err = cluster.SSTRunSendFile(client, backupfile, sv, progress)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Backup failed to send, closing connection!")
		return fmt.Errorf("Error sending SST to server %s: %s ", sv.Host, err.Error())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "Backup has been sent via SSL, closing connection!")
	}
	return nil
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
