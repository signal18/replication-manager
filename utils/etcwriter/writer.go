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
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/selfexec"
)

// Command is the hidden subcommand of replication-manager that serves the writes (Serve).
const Command = "etc-credential-writer"

// Request asks to replace Key's value: Plain, or Encrypted (the "hash_..." form) where the file
// holds it encrypted. Never logged: it carries a secret.
type Request struct {
	Key       string `json:"key"`
	Plain     string `json:"plain"`
	Encrypted string `json:"encrypted"`
}

// Response is the files rewritten, or why not.
type Response struct {
	Files []string `json:"files"`
	Err   string   `json:"err,omitempty"`
}

// Serve answers each request read on in, one JSON line each, with the files it rewrote on out.
// It writes only files, a key allowed says is a server secret, and only where the key is set.
// It runs as root in the child: it trusts nothing in a request beyond the key and the value.
func Serve(in io.Reader, out io.Writer, files []string, allowed func(key string) bool) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil // the parent closed the pipe: it is gone or stopping
			}
			return err
		}
		res := handle(req, files, allowed)
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
}

// handle is one request: the checks, then each file where the key is set.
func handle(req Request, files []string, allowed func(key string) bool) Response {
	var res Response
	switch {
	case !allowed(req.Key):
		res.Err = fmt.Sprintf("%s is not a server credential", req.Key)
	case ValidValue(req.Plain) != nil:
		res.Err = ValidValue(req.Plain).Error()
	case req.Encrypted != "" && ValidValue(req.Encrypted) != nil:
		res.Err = "encrypted form: " + ValidValue(req.Encrypted).Error()
	default:
		var errs []string
		for _, f := range files {
			changed, err := ReplaceInFile(f, req.Key, req.Plain, req.Encrypted)
			if err != nil {
				errs = append(errs, filepath.Base(f)+": "+err.Error())
				continue
			}
			if changed {
				res.Files = append(res.Files, f)
			}
		}
		if len(errs) > 0 {
			res.Err = fmt.Sprint(errs)
		}
	}
	return res
}

// Writer asks the child (or, in a process that stays root, this process) to write.
type Writer struct {
	mu      sync.Mutex
	files   []string
	allowed func(key string) bool
	local   bool // the process itself stays root: no child
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	dead    error
}

// Local is a Writer for a process that keeps root: it writes the files itself, with the same
// checks as the child.
func Local(files []string, allowed func(key string) bool) *Writer {
	return &Writer{files: files, allowed: allowed, local: true}
}

// Start starts the child, replication-manager's own binary run with the Command and the files
// as arguments, while the caller is still root. The child reads the requests on its stdin and
// answers on its file descriptor 3 (its stdout and stderr go to replication-manager's stderr,
// never mixed with the answers). It ends with the caller (selfexec.ChildAttr).
func Start(files []string) (*Writer, error) {
	cmd := exec.Command(selfexec.Path(), append([]string{Command}, files...)...)
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
	if err := cmd.Start(); err != nil {
		answers.Close()
		answersW.Close()
		return nil, err
	}
	answersW.Close() // the child's copy stays open
	w := &Writer{files: files, cmd: cmd, in: in, out: bufio.NewReader(answers)}
	go func() {
		err := cmd.Wait()
		w.mu.Lock()
		if w.dead == nil {
			w.dead = fmt.Errorf("the writer process ended: %v", err)
		}
		w.mu.Unlock()
	}()
	return w, nil
}

// Files are the files the writer may rewrite.
func (w *Writer) Files() []string { return append([]string(nil), w.files...) }

// Replace asks for key's value to be replaced, within timeout. A timeout or a broken pipe
// stops the child: its answers would no longer match the questions.
func (w *Writer) Replace(key, plain, encrypted string, timeout time.Duration) ([]string, error) {
	if w == nil {
		return nil, errors.New("no writer")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.local {
		res := handle(Request{Key: key, Plain: plain, Encrypted: encrypted}, w.files, w.allowed)
		return res.Files, errorOf(res)
	}
	if w.dead != nil {
		return nil, w.dead
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
			return nil, a.err
		}
		return a.res.Files, errorOf(a.res)
	case <-time.After(timeout):
		err := fmt.Errorf("the writer did not answer within %s", timeout)
		w.stop(err)
		return nil, err
	}
}

// Close ends the child: its stdin closed, it returns; killed if it does not.
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
