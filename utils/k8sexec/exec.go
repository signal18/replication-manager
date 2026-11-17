// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package k8sexec runs one bounded command in one container of a Pod through the Kubernetes API
// (pods/exec, client-go remotecommand). It is a Go helper for replication-manager's own code, with no HTTP
// route and no shell of its own: the caller passes an argument vector. One call is one command: no stdin,
// no TTY, an upper bound on the time and on the bytes it returns. It exists for observations that only an
// execution inside a container can make (what a running container sees under /etc/mysql); everything the
// API server or the database can say is read there instead.
//
// The identity that uses it needs the RBAC verb "create" on the subresource "pods/exec". The product's own
// Role does not grant it; the Kubernetes regtests document and check it (#2395).
package k8sexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	apiv1 "k8s.io/api/core/v1"
)

const (
	DefaultTimeout   = 30 * time.Second
	MaxTimeout       = 5 * time.Minute
	DefaultMaxOutput = 64 << 10
	MaxMaxOutput     = 1 << 20
)

// Kind is the class of a failure, so that a caller can tell a product problem from a permission, a missing
// container or the infrastructure.
type Kind string

const (
	KindDenied      Kind = "DENIED"      // the identity may not exec (RBAC), or is not authenticated
	KindNotFound    Kind = "NOT_FOUND"   // no such Pod
	KindUnavailable Kind = "UNAVAILABLE" // no such container, or it is not running (terminated, replaced)
	KindTimeout     Kind = "TIMEOUT"     // the deadline of the call (or of its context) passed
	KindCanceled    Kind = "CANCELED"    // the context was canceled
	KindExit        Kind = "EXIT"        // the command ran and exited with a non-zero status
	KindInfra       Kind = "INFRA"       // the connection, the API server or the runtime failed
	KindInvalid     Kind = "INVALID"     // the request was refused before anything was sent
)

// Error is the failure of one execution. For KindExit the Result is complete.
type Error struct {
	Kind   Kind
	Target Target
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("exec in %s/%s container %s: %s: %v", e.Target.Namespace, e.Target.Pod, e.Target.Container, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the Kind of an error returned by Run, "" for nil or an error of another origin.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// Target names the container to run in. The Pod is named, never selected: a caller that follows a
// replaced Pod reads the current one first.
type Target struct{ Namespace, Pod, Container string }

// Options bound one call. Zero values take the defaults.
type Options struct {
	Timeout   time.Duration // default 30s, at most 5m
	MaxOutput int           // bytes kept per stream, default 64 KiB, at most 1 MiB; the rest is dropped
}

// Result is what the command wrote. A stream that exceeded MaxOutput is cut and flagged.
type Result struct {
	Stdout, Stderr                   string
	StdoutTruncated, StderrTruncated bool
	ExitCode                         int
}

// newExecutor is replaced by the tests.
var newExecutor = func(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	return remotecommand.NewSPDYExecutor(cfg, "POST", u)
}

// Run runs command (an argument vector, never a shell string built from input) in the container and waits
// for it. A non-zero exit is an *Error of KindExit with the Result filled in.
func Run(ctx context.Context, cfg *rest.Config, t Target, command []string, opt Options) (Result, error) {
	fail := func(k Kind, err error) (Result, error) {
		return Result{ExitCode: -1}, &Error{Kind: k, Target: t, Err: err}
	}
	switch {
	case cfg == nil:
		return fail(KindInvalid, errors.New("no REST configuration"))
	case t.Namespace == "" || t.Pod == "" || t.Container == "":
		return fail(KindInvalid, errors.New("namespace, Pod and container are required"))
	case len(command) == 0 || command[0] == "":
		return fail(KindInvalid, errors.New("an empty command"))
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}
	if opt.Timeout > MaxTimeout {
		opt.Timeout = MaxTimeout
	}
	if opt.MaxOutput <= 0 {
		opt.MaxOutput = DefaultMaxOutput
	}
	if opt.MaxOutput > MaxMaxOutput {
		opt.MaxOutput = MaxMaxOutput
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fail(KindInfra, err)
	}
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(t.Namespace).Name(t.Pod).SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: t.Container,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)
	exec, err := newExecutor(cfg, req.URL())
	if err != nil {
		return fail(KindInfra, err)
	}

	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	stdout, stderr := &limitedBuffer{max: opt.MaxOutput}, &limitedBuffer{max: opt.MaxOutput}
	serr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: stdout, Stderr: stderr})
	// the transport's copy goroutines may still be writing (a context expiry returns before they stop):
	// the result is a synchronized snapshot, never a read of the buffers' state
	res := Result{}
	res.Stdout, res.StdoutTruncated = stdout.snapshot()
	res.Stderr, res.StderrTruncated = stderr.snapshot()
	if serr == nil {
		return res, nil
	}
	res.ExitCode = -1
	var ee utilexec.ExitError
	if errors.As(serr, &ee) && ee.Exited() {
		res.ExitCode = ee.ExitStatus()
		return res, &Error{Kind: KindExit, Target: t, Err: serr}
	}
	return res, &Error{Kind: classify(ctx, serr), Target: t, Err: serr}
}

// classify names the cause of a failed stream by the structure of the error where there is one, and by the
// text of the API server's refusal where the transport hides it (an upgrade that failed carries the status
// only as text).
func classify(ctx context.Context, err error) Kind {
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return KindTimeout
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return KindCanceled
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		return KindDenied
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "unauthorized"):
		return KindDenied
	case strings.Contains(msg, "container not found") || strings.Contains(msg, "is not valid for pod") ||
		strings.Contains(msg, "cannot exec in a stopped state") || strings.Contains(msg, "container is not running") ||
		strings.Contains(msg, "is terminated") || strings.Contains(msg, "is waiting to start"):
		return KindUnavailable
	case apierrors.IsNotFound(err) || (strings.Contains(msg, "pods ") && strings.Contains(msg, "not found")):
		return KindNotFound
	}
	return KindInfra
}

// limitedBuffer keeps the first max bytes and drops the rest without failing the stream: the command must
// still finish and report its exit status. It is written by a copy goroutine of the transport, which on a
// context expiry goes on writing after StreamWithContext has returned, while Run reads the result: every
// access to its state is under the mutex, and snapshot seals it so that nothing written later changes what
// Run returned, or grows the buffer.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
	sealed    bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return len(p), nil // the result was taken: late output is dropped
	}
	room := b.max - b.buf.Len()
	if room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:room])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// snapshot returns a consistent copy of the output and of the truncation flag, and seals the buffer.
func (b *limitedBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sealed = true
	return b.buf.String(), b.truncated
}
