package k8sexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// fakeExec is an Executor that runs a function instead of opening a connection.
type fakeExec struct {
	run func(ctx context.Context, so remotecommand.StreamOptions) error
}

func (f fakeExec) Stream(so remotecommand.StreamOptions) error {
	return f.run(context.Background(), so)
}
func (f fakeExec) StreamWithContext(ctx context.Context, so remotecommand.StreamOptions) error {
	return f.run(ctx, so)
}

// withFake replaces the executor for one test and records the URL it was asked for.
func withFake(t *testing.T, run func(ctx context.Context, so remotecommand.StreamOptions) error) *[]*url.URL {
	t.Helper()
	var mu sync.Mutex
	var urls []*url.URL
	old := newExecutor
	newExecutor = func(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
		mu.Lock()
		urls = append(urls, u)
		mu.Unlock()
		return fakeExec{run}, nil
	}
	t.Cleanup(func() { newExecutor = old })
	return &urls
}

func cfgFor(host string) *rest.Config { return &rest.Config{Host: host} }

var tgt = Target{Namespace: "ns1", Pod: "db1-abc", Container: "db1"}

func TestRunRequestShapeAndOutput(t *testing.T) {
	urls := withFake(t, func(_ context.Context, so remotecommand.StreamOptions) error {
		if so.Stdin != nil || so.Tty {
			t.Errorf("stdin or tty requested: %+v", so)
		}
		fmt.Fprint(so.Stdout, "out-text")
		fmt.Fprint(so.Stderr, "err-text")
		return nil
	})
	res, err := Run(context.Background(), cfgFor("http://127.0.0.1:1"), tgt, []string{"sh", "-c", "echo hi"}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "out-text" || res.Stderr != "err-text" || res.ExitCode != 0 {
		t.Fatalf("stdout/stderr/exit not captured: %+v", res)
	}
	u := (*urls)[0]
	if u.Path != "/api/v1/namespaces/ns1/pods/db1-abc/exec" {
		t.Errorf("path %q", u.Path)
	}
	q := u.Query()
	if got := q["command"]; len(got) != 3 || got[0] != "sh" || got[1] != "-c" || got[2] != "echo hi" {
		t.Errorf("command not passed as the argument vector, in order: %v", got)
	}
	for k, want := range map[string]string{"container": "db1", "stdout": "true", "stderr": "true"} {
		if got := q.Get(k); got != want {
			t.Errorf("query %s=%q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"stdin", "tty"} { // false options are not sent: no stdin, no TTY
		if q.Get(k) != "" {
			t.Errorf("query %s=%q, want it absent", k, q.Get(k))
		}
	}
}

func TestRunNonZeroExitKeepsOutput(t *testing.T) {
	withFake(t, func(_ context.Context, so remotecommand.StreamOptions) error {
		fmt.Fprint(so.Stdout, "partial")
		fmt.Fprint(so.Stderr, "boom")
		return utilexec.CodeExitError{Err: errors.New("command terminated with exit code 3"), Code: 3}
	})
	res, err := Run(context.Background(), cfgFor("http://x"), tgt, []string{"false"}, Options{})
	if KindOf(err) != KindExit {
		t.Fatalf("kind %q, want EXIT: %v", KindOf(err), err)
	}
	if res.ExitCode != 3 || res.Stdout != "partial" || res.Stderr != "boom" {
		t.Fatalf("exit status or output lost: %+v", res)
	}
}

func TestRunOutputIsBounded(t *testing.T) {
	withFake(t, func(_ context.Context, so remotecommand.StreamOptions) error {
		for i := 0; i < 100; i++ {
			fmt.Fprint(so.Stdout, strings.Repeat("a", 100))
		}
		fmt.Fprint(so.Stderr, "e")
		return nil
	})
	res, err := Run(context.Background(), cfgFor("http://x"), tgt, []string{"x"}, Options{MaxOutput: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 1000 || !res.StdoutTruncated || res.StderrTruncated {
		t.Fatalf("stdout %d bytes truncated=%v stderr truncated=%v", len(res.Stdout), res.StdoutTruncated, res.StderrTruncated)
	}
	// the caps themselves are capped
	withFake(t, func(_ context.Context, so remotecommand.StreamOptions) error {
		fmt.Fprint(so.Stdout, strings.Repeat("b", MaxMaxOutput+10))
		return nil
	})
	res, _ = Run(context.Background(), cfgFor("http://x"), tgt, []string{"x"}, Options{MaxOutput: 1 << 30})
	if len(res.Stdout) != MaxMaxOutput || !res.StdoutTruncated {
		t.Fatalf("MaxOutput not capped: %d", len(res.Stdout))
	}
}

func TestRunTimeoutAndCancellation(t *testing.T) {
	block := func(ctx context.Context, _ remotecommand.StreamOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}
	withFake(t, block)
	start := time.Now()
	_, err := Run(context.Background(), cfgFor("http://x"), tgt, []string{"sleep", "100"}, Options{Timeout: 50 * time.Millisecond})
	if KindOf(err) != KindTimeout || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: kind %q after %s: %v", KindOf(err), time.Since(start), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err = Run(ctx, cfgFor("http://x"), tgt, []string{"sleep", "100"}, Options{Timeout: time.Minute})
	if KindOf(err) != KindCanceled {
		t.Fatalf("cancel: kind %q: %v", KindOf(err), err)
	}
	// the executor that ignores its context but whose error is a deadline: still a timeout, by the context
	withFake(t, func(_ context.Context, _ remotecommand.StreamOptions) error {
		time.Sleep(80 * time.Millisecond)
		return errors.New("connection closed")
	})
	_, err = Run(context.Background(), cfgFor("http://x"), tgt, []string{"x"}, Options{Timeout: 20 * time.Millisecond})
	if KindOf(err) != KindTimeout {
		t.Fatalf("expired context with another error: kind %q", KindOf(err))
	}
}

func TestRunRejectsBadRequestsBeforeAnything(t *testing.T) {
	called := false
	withFake(t, func(context.Context, remotecommand.StreamOptions) error { called = true; return nil })
	for name, c := range map[string]struct {
		cfg *rest.Config
		tg  Target
		cmd []string
	}{
		"no config":    {nil, tgt, []string{"x"}},
		"no namespace": {cfgFor("http://x"), Target{Pod: "p", Container: "c"}, []string{"x"}},
		"no pod":       {cfgFor("http://x"), Target{Namespace: "n", Container: "c"}, []string{"x"}},
		"no container": {cfgFor("http://x"), Target{Namespace: "n", Pod: "p"}, []string{"x"}},
		"no command":   {cfgFor("http://x"), tgt, nil},
		"empty argv0":  {cfgFor("http://x"), tgt, []string{""}},
	} {
		if _, err := Run(context.Background(), c.cfg, c.tg, c.cmd, Options{}); KindOf(err) != KindInvalid {
			t.Errorf("%s: kind %q, want INVALID", name, KindOf(err))
		}
	}
	if called {
		t.Error("an invalid request reached the executor")
	}
}

func TestRunConcurrentCallsDoNotShareState(t *testing.T) {
	withFake(t, func(_ context.Context, so remotecommand.StreamOptions) error {
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(so.Stdout, "x")
		return nil
	})
	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tg := Target{Namespace: "ns", Pod: fmt.Sprintf("p%d", i), Container: "c"}
			res, err := Run(context.Background(), cfgFor("http://x"), tg, []string{"echo", fmt.Sprint(i)}, Options{})
			if err != nil || res.Stdout != "x" {
				errs <- fmt.Sprintf("call %d: %v %+v", i, err, res)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// The next tests use the real SPDY executor against an API server that refuses: they show the shape of the
// errors the classification has to read (RBAC denial, no such Pod, no such container, unreachable server).
func apiRefusing(t *testing.T, code int, reason metav1.StatusReason, message string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure, Code: int32(code), Reason: reason, Message: message,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunClassifiesRefusalsOfTheAPIServer(t *testing.T) {
	for _, c := range []struct {
		name    string
		code    int
		reason  metav1.StatusReason
		message string
		want    Kind
	}{
		{"RBAC denial", 403, metav1.StatusReasonForbidden, `pods "db1-abc" is forbidden: User "u" cannot create resource "pods/exec" in API group "" in the namespace "ns1"`, KindDenied},
		{"not authenticated", 401, metav1.StatusReasonUnauthorized, "Unauthorized", KindDenied},
		{"no such Pod", 404, metav1.StatusReasonNotFound, `pods "db1-abc" not found`, KindNotFound},
		{"no such container", 400, metav1.StatusReasonBadRequest, `container nosuch is not valid for pod db1-abc`, KindUnavailable},
		{"stopped container", 400, metav1.StatusReasonBadRequest, `cannot exec in a stopped state: unknown`, KindUnavailable},
		{"server error", 500, metav1.StatusReasonInternalError, "boom", KindInfra},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := apiRefusing(t, c.code, c.reason, c.message)
			_, err := Run(context.Background(), cfgFor(srv.URL), tgt, []string{"true"}, Options{Timeout: 10 * time.Second})
			if err == nil {
				t.Fatal("no error")
			}
			if KindOf(err) != c.want {
				t.Fatalf("kind %q, want %q: %v", KindOf(err), c.want, err)
			}
		})
	}
}

func TestRunUnreachableServerIsInfrastructure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	_, err := Run(context.Background(), cfgFor(url), tgt, []string{"true"}, Options{Timeout: 5 * time.Second})
	if KindOf(err) != KindInfra {
		t.Fatalf("kind %q, want INFRA: %v", KindOf(err), err)
	}
}

// A writer that is still active when the stream returns: client-go's StreamWithContext returns on a context
// expiry while its copy goroutines go on writing until the connection is closed. The result is read by Run
// at that moment, so every access to the buffers must be synchronized, and what Run returned must not
// change after it returned.
func TestRunWriterStillActiveAfterTheStreamReturns(t *testing.T) {
	for name, trigger := range map[string]func(cancel context.CancelFunc) Options{
		"timeout": func(context.CancelFunc) Options { return Options{Timeout: 40 * time.Millisecond, MaxOutput: 1000} },
		"cancellation": func(cancel context.CancelFunc) Options {
			time.AfterFunc(40*time.Millisecond, cancel)
			return Options{Timeout: time.Minute, MaxOutput: 1000}
		},
	} {
		t.Run(name, func(t *testing.T) {
			stop := make(chan struct{})
			var writers sync.WaitGroup
			started := make(chan struct{})
			withFake(t, func(ctx context.Context, so remotecommand.StreamOptions) error {
				writers.Add(1)
				go func() { // the copy goroutine of the transport: it outlives the stream
					defer writers.Done()
					close(started)
					for {
						select {
						case <-stop:
							return
						default:
							fmt.Fprint(so.Stdout, "xxxxxxxxxx")
							fmt.Fprint(so.Stderr, "e")
						}
					}
				}()
				<-started // it has written, and goes on writing, when the stream returns
				<-ctx.Done()
				return ctx.Err()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := Run(ctx, cfgFor("http://x"), tgt, []string{"x"}, trigger(cancel))
			want := KindTimeout
			if name == "cancellation" {
				want = KindCanceled
			}
			if KindOf(err) != want {
				t.Fatalf("kind %q, want %q: %v", KindOf(err), want, err)
			}
			// the result is a consistent copy: bounded, flagged, and unchanged while the writer goes on
			first := res
			time.Sleep(20 * time.Millisecond)
			close(stop)
			writers.Wait()
			if res != first {
				t.Errorf("the Result changed after Run returned: %+v then %+v", first, res)
			}
			if len(res.Stdout) > 1000 || len(res.Stderr) > 1000 {
				t.Errorf("output over the bound: %d, %d", len(res.Stdout), len(res.Stderr))
			}
			if len(res.Stdout) == 1000 && !res.StdoutTruncated {
				t.Error("a full buffer that was written past must be flagged truncated")
			}
		})
	}
}
