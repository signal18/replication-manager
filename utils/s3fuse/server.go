// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
)

// The mount's server is a child process of this binary (#2246): ServerCommand, started from
// the running binary. The parent writes on its stdin, one JSON message per line: the mount,
// then the S3 credentials, again each time they are renewed; stdin closed asks it to flush,
// unmount and exit. The child answers on its file descriptor 3, one JSON event per line
// (mounted, late, panic, unmounted, failed); what it prints (GeeseFS's log, a crash) goes, line
// by line, to Options.OnLog. Credentials never go in the arguments, the environment or a log.

// ServerCommand is the command of this binary that serves a mount (hidden).
const ServerCommand = "backup-folder-mount"

// serverStartTimeout bounds a mount's start: GeeseFS's first listing of the bucket included
const serverStartTimeout = 3 * time.Minute

// serverExitTimeout bounds the wait for a server asked to stop, its unmount done or refused
const serverExitTimeout = 30 * time.Second

// serverBusyBound bounds a server whose unmount was refused: it serves the detached mount until
// the last file is closed, at most that long; its exit then fails what still waits (ENOTCONN)
const serverBusyBound = lateGrace

// logLineMax bounds each line of the server's output passed on (F4)
const logLineMax = 2000

// eventLineMax bounds an event line, far above any event (an error is cut at logLineMax): an
// event cut would not parse and would be lost
const eventLineMax = 64 << 10

// serverEnv are the variables of the environment the server keeps: what it runs (fusermount)
// and how it reaches S3; nothing else of replication-manager's environment, secrets included
// (F10)
var serverEnv = []string{"PATH", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR", "TMPDIR"}

type mountRequest struct {
	MountPoint  string        `json:"mountPoint"`
	Bucket      string        `json:"bucket"`
	Endpoint    string        `json:"endpoint"`
	Region      string        `json:"region"`
	HTTPTimeout time.Duration `json:"httpTimeout"`
	OpTimeout   time.Duration `json:"opTimeout"`
	MemoryLimit uint64        `json:"memoryLimit"`
	Debug       bool          `json:"debug"`
}

type credentialsMessage struct {
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	SessionToken    string    `json:"sessionToken,omitempty"`
	Expires         time.Time `json:"expires,omitempty"`
}

// request is a message from the parent: one of its fields set
type request struct {
	Mount       *mountRequest       `json:"mount,omitempty"`
	Credentials *credentialsMessage `json:"credentials,omitempty"`
}

const (
	eventMounted   = "mounted"
	eventLate      = "late"
	eventPanic     = "panic"
	eventUnmounted = "unmounted" // Error set: the unmount was refused
	eventFailed    = "failed"    // the mount could not be made
)

type event struct {
	Event string `json:"event"`
	Op    string `json:"op,omitempty"`
	Error string `json:"error,omitempty"`
}

// server is the parent's side of a mount's server.
type server struct {
	stdin   io.WriteCloser
	writeMu sync.Mutex // one message at a time on stdin; never held to close it
	stop    sync.Once
	unmount func() error
	// unmounted gets the server's answer to the stop
	unmounted chan error
	exited    chan struct{} // closed once the server exited, exitErr set
	exitErr   error
}

func (s *server) send(r request) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

// startServer starts the mount's server and returns once it mounted, or why it did not.
func startServer(ctx context.Context, o Options) (*server, error) {
	cmd := exec.Command(selfExecutable(), ServerCommand)
	cmd.Args[0] = "replication-manager" // ps shows what it is, not /proc/self/exe
	cmd.Env = []string{}
	for _, k := range serverEnv {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	evR, evW, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	logR, logW, err := os.Pipe()
	if err != nil {
		stdin.Close()
		evR.Close()
		evW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = logW, logW
	cmd.ExtraFiles = []*os.File{evW}
	cmd.SysProcAttr = serverProcAttr()

	s := &server{stdin: stdin, unmounted: make(chan error, 1), exited: make(chan struct{})}
	waited := make(chan error, 1)
	if err := startLocked(cmd, waited); err != nil {
		stdin.Close()
		evR.Close()
		evW.Close()
		logR.Close()
		logW.Close()
		return nil, fmt.Errorf("s3fuse: starting the mount's server: %w", err)
	}
	// the child holds its own ends: ours closed, its exit ends both readers
	evW.Close()
	logW.Close()

	var last lastLine
	logsDone := make(chan struct{})
	go func() {
		readLines(logR, logLineMax, func(line string) {
			last.set(line)
			if o.OnLog != nil {
				o.OnLog(line)
			}
		})
		logR.Close()
		close(logsDone)
	}()
	mountedCh := make(chan error, 1)
	eventsDone := make(chan struct{})
	go func() {
		readLines(evR, eventLineMax, func(line string) {
			var e event
			if json.Unmarshal([]byte(line), &e) != nil {
				return
			}
			switch e.Event {
			case eventMounted:
				mountedCh <- nil
			case eventFailed:
				mountedCh <- errors.New(e.Error)
			case eventLate:
				if o.OnLate != nil {
					o.OnLate(e.Op)
				}
			case eventPanic:
				if o.OnPanic != nil {
					o.OnPanic(e.Op, e.Error, nil) // the stack is in the server's output
				}
			case eventUnmounted:
				if e.Error != "" {
					s.unmounted <- errors.New(e.Error)
				} else {
					s.unmounted <- nil
				}
			}
		})
		evR.Close()
		close(eventsDone)
	}()
	go func() {
		err := <-waited
		<-eventsDone
		<-logsDone
		if err != nil {
			s.exitErr = fmt.Errorf("the mount's server ended: %v; its last output: %s", err, last.get())
		} else {
			s.exitErr = errors.New("the mount's server ended")
		}
		close(s.exited)
	}()

	s.unmount = func() error {
		s.stop.Do(func() { stdin.Close() })
		select {
		case err := <-s.unmounted:
			if err != nil {
				return err
			}
		case <-s.exited:
			return nil
		case <-time.After(backupUnmountTimeout):
			return fmt.Errorf("the mount's server did not unmount within %s", backupUnmountTimeout)
		}
		select {
		case <-s.exited:
		case <-time.After(serverExitTimeout):
			cmd.Process.Kill()
		}
		return nil
	}

	req := mountRequest{MountPoint: o.MountPoint, Bucket: o.Bucket, Endpoint: o.Endpoint, Region: o.Region,
		HTTPTimeout: o.HTTPTimeout, OpTimeout: o.OpTimeout, MemoryLimit: o.MemoryLimit, Debug: o.Debug}
	fail := func(err error) (*server, error) {
		cmd.Process.Kill()
		<-s.exited
		return nil, err
	}
	if err := s.send(request{Mount: &req}); err != nil {
		return fail(fmt.Errorf("s3fuse: the mount's server: %w", err))
	}
	v, err := o.Credentials.Get()
	if err != nil {
		return fail(fmt.Errorf("s3fuse: %s: credentials: %w", o.Bucket, err))
	}
	if err := s.send(request{Credentials: credentialsOf(v, o.Credentials)}); err != nil {
		return fail(fmt.Errorf("s3fuse: the mount's server: %w", err))
	}
	timer := time.NewTimer(serverStartTimeout)
	defer timer.Stop()
	select {
	case err := <-mountedCh:
		if err != nil {
			return fail(err)
		}
	case <-s.exited:
		return nil, s.exitErr
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		return fail(fmt.Errorf("s3fuse: %s not mounted within %s", o.Bucket, serverStartTimeout))
	}
	go s.renewCredentials(o.Credentials, v)
	return s, nil
}

// backupUnmountTimeout bounds the server's answer to a stop: GeeseFS's flush and its unmount,
// retried 20 s when busy
const backupUnmountTimeout = 50 * time.Second

// startLocked starts cmd from a goroutine locked to its thread for the child's whole life: the
// child's parent-death signal follows the thread that started it, not the process, and Go
// would otherwise end or reuse that thread. waited gets cmd.Wait's result.
func startLocked(cmd *exec.Cmd, waited chan<- error) error {
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with this goroutine
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		waited <- cmd.Wait()
	}()
	return <-started
}

// credentialsInterval: the credentials are checked that often and sent again when renewed
const credentialsInterval = 30 * time.Second

// renewCredentials sends the server the credentials each time they are renewed, until it exits.
func (s *server) renewCredentials(creds *credentials.Credentials, last credentials.Value) {
	t := time.NewTicker(credentialsInterval)
	defer t.Stop()
	for {
		select {
		case <-s.exited:
			return
		case <-t.C:
		}
		v, err := creds.Get() // renewed when near their end (the SSO)
		if err != nil || v == last {
			continue
		}
		if s.send(request{Credentials: credentialsOf(v, creds)}) == nil {
			last = v
		}
	}
}

func credentialsOf(v credentials.Value, creds *credentials.Credentials) *credentialsMessage {
	m := &credentialsMessage{AccessKeyID: v.AccessKeyID, SecretAccessKey: v.SecretAccessKey, SessionToken: v.SessionToken}
	if at, err := creds.ExpiresAt(); err == nil {
		m.Expires = at
	}
	return m
}

// lastLine keeps the server's last line of output, for the error of its end.
type lastLine struct {
	mu   sync.Mutex
	line string
}

func (l *lastLine) set(s string) {
	if s == "" {
		return
	}
	l.mu.Lock()
	l.line = s
	l.mu.Unlock()
}

func (l *lastLine) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.line
}

// readLines calls f with each line of r, cut at max bytes (the rest of a longer line dropped),
// until r ends: a line of any length never stops the reading, so the writer never blocks.
func readLines(r io.Reader, max int, f func(string)) {
	br := bufio.NewReaderSize(r, 4096)
	var line []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(line) < max {
			line = append(line, chunk[:min(len(chunk), max-len(line))]...)
		}
		if err != nil {
			if len(line) > 0 {
				f(string(line))
			}
			return
		}
		if !isPrefix {
			f(string(line))
			line = line[:0]
		}
	}
}

// relayedCredentials, in the server, are the last credentials the parent sent.
type relayedCredentials struct {
	mu        sync.Mutex
	value     credentials.Value
	expires   time.Time
	version   int
	retrieved int
}

func (r *relayedCredentials) set(m *credentialsMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.value = credentials.Value{AccessKeyID: m.AccessKeyID, SecretAccessKey: m.SecretAccessKey, SessionToken: m.SessionToken, ProviderName: "replication-manager"}
	r.expires = m.Expires
	r.version++
}

func (r *relayedCredentials) Retrieve() (credentials.Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.version == 0 {
		return credentials.Value{}, errors.New("no credentials received")
	}
	r.retrieved = r.version
	return r.value, nil
}

// IsExpired: new ones were sent, or these reached their end (the last ones kept meanwhile).
func (r *relayedCredentials) IsExpired() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retrieved != r.version || (!r.expires.IsZero() && time.Now().After(r.expires))
}

var _ credentials.Provider = (*relayedCredentials)(nil)

// Serve is the mount's server, the ServerCommand: it reads the parent's messages on in, writes
// its events on events, mounts and serves until in is closed (or SIGTERM: the parent died),
// then flushes, unmounts and returns.
func Serve(in io.Reader, events io.Writer) error {
	var emu sync.Mutex
	emit := func(e event) {
		if len(e.Error) > logLineMax {
			e.Error = e.Error[:logLineMax]
		}
		b, _ := json.Marshal(e)
		emu.Lock()
		events.Write(append(b, '\n'))
		emu.Unlock()
	}
	dec := json.NewDecoder(in)
	var req mountRequest
	relayed := &relayedCredentials{}
	for req.MountPoint == "" || relayed.version == 0 {
		var r request
		if err := dec.Decode(&r); err != nil {
			return fmt.Errorf("s3fuse: reading the mount: %w", err)
		}
		if r.Mount != nil {
			req = *r.Mount
		}
		if r.Credentials != nil {
			relayed.set(r.Credentials)
		}
	}
	o := Options{MountPoint: req.MountPoint, Bucket: req.Bucket, Endpoint: req.Endpoint, Region: req.Region,
		Credentials: credentials.NewCredentials(relayed), HTTPTimeout: req.HTTPTimeout, OpTimeout: req.OpTimeout,
		MemoryLimit: req.MemoryLimit, Debug: req.Debug,
		OnLate: func(op string) { emit(event{Event: eventLate, Op: op}) },
		OnPanic: func(op string, r interface{}, stack []byte) {
			fmt.Fprintf(os.Stderr, "%s panicked, answered EIO: %v\n%s\n", op, r, stack)
			emit(event{Event: eventPanic, Op: op, Error: fmt.Sprint(r)})
		},
	}
	s, err := serve(context.Background(), o)
	if err != nil {
		emit(event{Event: eventFailed, Error: err.Error()})
		return err
	}
	emit(event{Event: eventMounted})

	stop := make(chan struct{})
	go func() {
		for {
			var r request
			if err := dec.Decode(&r); err != nil {
				close(stop) // stdin closed: the parent asks to stop, or it is gone
				return
			}
			if r.Credentials != nil {
				relayed.set(r.Credentials)
			}
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	ended := make(chan struct{})
	go func() {
		s.mfs.Join(context.Background())
		close(ended)
	}()
	select {
	case <-stop:
	case <-sig:
	case <-ended: // unmounted from outside
		s.fs.Shutdown()
		return nil
	}
	if err := s.unmount(req.MountPoint); err != nil {
		// busy: served until the parent's detach is released, bounded
		emit(event{Event: eventUnmounted, Error: err.Error()})
		select {
		case <-ended:
		case <-time.After(serverBusyBound):
		}
		s.fs.Shutdown()
		return err
	}
	emit(event{Event: eventUnmounted})
	return nil
}
