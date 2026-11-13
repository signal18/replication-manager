// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package etcwriter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/selfexec"
)

// Command is the hidden subcommand of replication-manager that serves the writes (Serve).
const Command = "etc-credential-writer"

// MaxValue bounds a value in a request, MaxRequest a request line (T18): a parent cannot make
// the root child allocate without limit.
const (
	MaxValue   = 16 << 10
	MaxRequest = 3*MaxValue + 1024
)

// Request asks to replace Key's value: Plain, or Encrypted (the "hash_..." form) where the file
// holds it encrypted. Never logged: it carries a secret.
type Request struct {
	Key       string `json:"key"`
	Plain     string `json:"plain"`
	Encrypted string `json:"encrypted"`
}

// Response says, per file, what the request did: Written the files rewritten, Set every file
// that sets the key in a [default] section (written, or already holding the value), Refused the
// files the writer does not write and why (unsafe, see Unsafe), Err what went wrong.
type Response struct {
	Written []string          `json:"written,omitempty"`
	Set     []string          `json:"set,omitempty"`
	Refused map[string]string `json:"refused,omitempty"`
	Err     string            `json:"err,omitempty"`
}

// checkValue: v fits a one-line string and the size bound.
func checkValue(v string) error {
	if len(v) > MaxValue {
		return fmt.Errorf("value longer than %d bytes", MaxValue)
	}
	return ValidValue(v)
}

// handle is one request: the checks, then each file.
func handle(req Request, files []string, refused map[string]string, allowed func(key string) bool, trail io.Writer) Response {
	res := Response{Refused: refused}
	switch {
	case !allowed(req.Key):
		res.Err = fmt.Sprintf("%s is not a server credential", req.Key)
	case checkValue(req.Plain) != nil:
		res.Err = checkValue(req.Plain).Error()
	case req.Encrypted != "" && checkValue(req.Encrypted) != nil:
		res.Err = "encrypted form: " + checkValue(req.Encrypted).Error()
	default:
		var errs []string
		for _, f := range files {
			found, changed, err := ReplaceInFile(f, req.Key, req.Plain, req.Encrypted)
			if err != nil {
				errs = append(errs, filepath.Base(f)+": "+err.Error())
				continue
			}
			if found {
				res.Set = append(res.Set, f)
			}
			if changed {
				res.Written = append(res.Written, f)
				if trail != nil {
					// the root side's own trail: the key and the file, never the value
					fmt.Fprintf(trail, "%s %s: %s rewritten in %s\n", time.Now().UTC().Format(time.RFC3339), Command, req.Key, f)
				}
			}
		}
		if len(errs) > 0 {
			res.Err = fmt.Sprint(errs)
		}
	}
	return res
}

// Split keeps the files uid and gid cannot swap (Unsafe), resolved, and says why for the others.
func Split(files []string, uid, gid int) ([]string, map[string]string) {
	var safe []string
	refused := map[string]string{}
	for _, f := range files {
		real, why := Resolve(f, uid, gid)
		if why != "" {
			refused[f] = why
			continue
		}
		safe = append(safe, real)
	}
	return safe, refused
}

// Serve answers each request read on in, one JSON line each, on out. It runs as root in the
// child and trusts nothing in a request beyond the key and the value: it writes only a key
// allowed says is a server credential, in the files it was given that uid and gid (the user the
// parent drops to) cannot swap, where the key is set. A request line longer than MaxRequest
// stops it.
func Serve(in io.Reader, out io.Writer, files []string, uid, gid int, allowed func(key string) bool) error {
	safe, refused := Split(files, uid, gid)
	for f, why := range refused {
		fmt.Fprintf(os.Stderr, "%s: %s is not written: %s\n", Command, f, why)
	}
	for _, f := range safe {
		RemoveStaleTemps(f)
	}
	r := bufio.NewReaderSize(in, MaxRequest)
	enc := json.NewEncoder(out)
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return fmt.Errorf("a request longer than %d bytes", MaxRequest)
		}
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				return nil // the parent closed the pipe: it is gone or stopping
			}
			return err
		}
		var req Request
		if jerr := json.Unmarshal(line, &req); jerr != nil {
			return jerr
		}
		if err := enc.Encode(handle(req, safe, refused, allowed, os.Stderr)); err != nil {
			return err
		}
	}
}

// Writer asks the child (or, in a process that keeps root, this process) to write.
type Writer struct {
	mu      sync.Mutex
	files   []string
	allowed func(key string) bool
	local   bool // the process itself keeps root: no child
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	dead    error
}

// Local is a Writer for a process that keeps root: it writes the files itself, with the same
// checks as the child (no user to drop to: every file is written).
func Local(files []string, allowed func(key string) bool) *Writer {
	return &Writer{files: files, allowed: allowed, local: true}
}

// Start starts the child, replication-manager's own binary run with the Command, the user it is
// about to drop to and the files as arguments, while the caller is still root. The child reads
// the requests on its stdin and answers on its file descriptor 3 (its stdout and stderr go to
// replication-manager's stderr, never mixed with the answers). It ends with the caller: started
// from a locked thread (selfexec.StartLocked), with the parent-death signal.
func Start(files []string, uid, gid int) (*Writer, error) {
	args := append([]string{Command, "--uid", strconv.Itoa(uid), "--gid", strconv.Itoa(gid)}, files...)
	cmd := exec.Command(selfexec.Path(), args...)
	cmd.SysProcAttr = selfexec.ChildAttr()
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	answers, answersW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.ExtraFiles = []*os.File{answersW}
	waited := make(chan error, 1)
	if err := selfexec.StartLocked(cmd, waited); err != nil {
		answers.Close()
		answersW.Close()
		return nil, err
	}
	answersW.Close() // the child's copy stays open
	w := &Writer{files: files, cmd: cmd, in: in, out: bufio.NewReader(answers)}
	go func() {
		err := <-waited
		w.mu.Lock()
		if w.dead == nil {
			w.dead = fmt.Errorf("the writer process ended: %v", err)
		}
		w.mu.Unlock()
	}()
	return w, nil
}

// Files are the files the writer was given.
func (w *Writer) Files() []string { return append([]string(nil), w.files...) }

// Replace asks for key's value to be replaced, within timeout. A timeout or a broken pipe stops
// the child (its answers would no longer match the questions): the writer is then gone until
// replication-manager restarts, and says so at each request.
func (w *Writer) Replace(key, plain, encrypted string, timeout time.Duration) (Response, error) {
	if w == nil {
		return Response{}, errors.New("no writer")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.local {
		res := handle(Request{Key: key, Plain: plain, Encrypted: encrypted}, w.files, nil, w.allowed, nil)
		return res, errorOf(res)
	}
	if w.dead != nil {
		return Response{}, w.dead
	}
	type answer struct {
		res Response
		err error
	}
	done := make(chan answer, 1)
	go func() {
		if err := json.NewEncoder(w.in).Encode(Request{Key: key, Plain: plain, Encrypted: encrypted}); err != nil {
			done <- answer{err: err}
			return
		}
		line, err := w.out.ReadBytes('\n')
		if err != nil {
			done <- answer{err: err}
			return
		}
		var res Response
		err = json.Unmarshal(line, &res)
		done <- answer{res: res, err: err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			w.stop(a.err)
			return Response{}, a.err
		}
		return a.res, errorOf(a.res)
	case <-time.After(timeout):
		err := fmt.Errorf("the writer did not answer within %s, stopped until the next start", timeout)
		w.stop(err)
		return Response{}, err
	}
}

// Close ends the child: its stdin closed, then killed.
func (w *Writer) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.in != nil {
		w.in.Close()
	}
	w.stop(fmt.Errorf("closed"))
}

// stop ends the child after a lost exchange, w.mu held.
func (w *Writer) stop(err error) {
	w.dead = err
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}
}

func errorOf(res Response) error {
	if res.Err != "" {
		return errors.New(res.Err)
	}
	return nil
}
