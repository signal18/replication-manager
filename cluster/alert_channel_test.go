// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/alert/alertmanager"
	"github.com/signal18/replication-manager/utils/state"
)

func newAlertTestCluster(t *testing.T) *Cluster {
	t.Helper()
	c := newTestClusterForUsers(t)
	c.StateMachine = new(state.StateMachine)
	c.StateMachine.Init()
	c.Status = ConstMonitorActif
	c.Conf.MonitoringAlertTrigger = "ERR00042,WARN0023"
	t.Cleanup(c.stopAlertChannels)
	return c
}

// blockingChannel is a channel whose sends wait until released: a dead backend.
type blockingChannel struct {
	release chan struct{}
	mu      sync.Mutex
	got     []AlertEvent
}

func (b *blockingChannel) Name() string               { return "blocking" }
func (b *blockingChannel) Enabled() bool              { return true }
func (b *blockingChannel) Accepts(ev AlertEvent) bool { return true }
func (b *blockingChannel) Timeout() time.Duration     { return time.Minute }
func (b *blockingChannel) Send(ctx context.Context, ev AlertEvent) error {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	b.mu.Lock()
	b.got = append(b.got, ev)
	b.mu.Unlock()
	return nil
}

// A dead channel never stalls the caller: the queue is bounded, the oldest alerts are
// dropped, the drops are counted and WARN0362 opens (F2, T18).
func TestAlertQueueBoundedDropOldest(t *testing.T) {
	c := newAlertTestCluster(t)
	c.Conf.AlertQueueLimit = 5
	b := &blockingChannel{release: make(chan struct{})}
	r := c.alertChannels()
	r.queues = []*alertQueue{{channel: b, wake: make(chan struct{}, 1)}}

	start := time.Now()
	for i := 0; i < 50; i++ {
		c.dispatchAlert(AlertEvent{Kind: AlertLogLine, Level: "ALERT", Message: string(rune('a' + i%26))})
	}
	if time.Since(start) > time.Second {
		t.Fatal("dispatchAlert waited on the channel")
	}
	q := r.queues[0]
	if n := q.len(); n > 5 {
		t.Fatalf("%d queued, limit 5", n)
	}
	if d := q.dropped.Load(); d < 44 {
		t.Fatalf("%d dropped, want at least 44 (50 - 5 queued - 1 in flight)", d)
	}
	c.CheckAlertChannels()
	if !c.StateMachine.CurState.Search("WARN0362") {
		t.Fatal("drops must open WARN0362")
	}
	close(b.release)
}

// A failing channel opens WARN0363 after alertFailureThreshold sends, with its error.
func TestAlertFailureState(t *testing.T) {
	c := newAlertTestCluster(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	defer srv.Close()
	c.Conf.AlertTelegram = true
	c.Conf.AlertTelegramBotToken = "111:MARKERTOKEN"
	c.Conf.Secrets.Set("alert-telegram-bot-token", config.Secret{Value: "111:MARKERTOKEN"})
	c.Conf.AlertTelegramChatID = "42"
	c.Conf.AlertTelegramAPIURL = srv.URL
	for i := 0; i < 3; i++ {
		c.CheckAlert(state.State{ErrKey: "ERR00042", ErrType: "ERROR", ErrDesc: "Master down"}, false)
	}
	q := alertQueueOf(t, c, ConstAlertChannelTelegram)
	waitFor(t, func() bool { return q.failures.Load() >= 3 })
	c.CheckAlertChannels()
	st, ok := (*c.StateMachine.CurState)["WARN0363"]
	if !ok || !strings.Contains(st.ErrDesc, "telegram") || !strings.Contains(st.ErrDesc, "Unauthorized") {
		t.Fatalf("WARN0363 %+v", st)
	}
	if strings.Contains(st.ErrDesc, "MARKER") {
		t.Fatalf("the bot token in a state: %s", st.ErrDesc)
	}
}

func alertQueueOf(t *testing.T, c *Cluster, name string) *alertQueue {
	t.Helper()
	for _, q := range c.alertChannels().queues {
		if q.channel.Name() == name {
			return q
		}
	}
	t.Fatalf("no channel %s", name)
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type fakeAlertmanager struct {
	*httptest.Server
	mu     sync.Mutex
	posts  [][]alertmanager.Alert
	header http.Header
}

func newFakeAlertmanager(t *testing.T) *fakeAlertmanager {
	f := &fakeAlertmanager{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a []alertmanager.Alert
		if r.URL.Path != "/api/v2/alerts" {
			t.Errorf("path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&a)
		f.mu.Lock()
		f.posts = append(f.posts, a)
		f.header = r.Header.Clone()
		f.mu.Unlock()
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAlertmanager) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

func (f *fakeAlertmanager) last() []alertmanager.Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[len(f.posts)-1]
}

// A state of monitoring-alert-trigger fires with its labels, and resolves with endsAt when it
// clears; a state outside the trigger is not sent.
func TestAlertmanagerFireAndResolve(t *testing.T) {
	c := newAlertTestCluster(t)
	am := newFakeAlertmanager(t)
	c.Conf.AlertAlertmanager = true
	c.Conf.AlertAlertmanagerURL = am.URL
	c.Conf.Secrets.Set("alert-alertmanager-bearer-token", config.Secret{Value: "MARKERBEARER"})
	st := state.State{ErrKey: "ERR00042", ErrType: "ERROR", ErrDesc: "Skip slave in election %s have no master log file", ServerUrl: "db1:3306"}

	c.CheckAlert(state.State{ErrKey: "WARN0999", ErrType: "WARN", ErrDesc: "not a trigger"}, false)
	c.CheckAlert(st, false)
	waitFor(t, func() bool { return am.count() == 1 })
	fired := am.last()[0]
	want := map[string]string{"alertname": "ERR00042", "code": "ERR00042", "cluster": "c1", "server": "db1:3306", "severity": "critical", "domain": "ha"}
	for k, v := range want {
		if fired.Labels[k] != v {
			t.Fatalf("label %s=%q, want %q (%v)", k, fired.Labels[k], v, fired.Labels)
		}
	}
	if fired.StartsAt == "" || fired.EndsAt != "" || fired.Annotations["summary"] == "" {
		t.Fatalf("firing alert %+v", fired)
	}
	if am.header.Get("Authorization") != "Bearer MARKERBEARER" {
		t.Fatalf("authorization %q", am.header.Get("Authorization"))
	}

	c.CheckAlert(st, true)
	waitFor(t, func() bool { return am.count() == 2 })
	resolved := am.last()[0]
	if resolved.EndsAt == "" || resolved.StartsAt != fired.StartsAt || resolved.Labels["server"] != "db1:3306" {
		t.Fatalf("resolve %+v", resolved)
	}
	ch := c.AlertChannel(ConstAlertChannelAlertmanager).(*alertmanagerAlertChannel)
	if ch.firingCount() != 0 {
		t.Fatal("a resolved alert must leave the firing set")
	}
}

// The resend posts the firing set again, adds a trigger state open in the state machine and
// missing from the set, and resolves an entry the state machine dropped for two rounds.
func TestAlertmanagerResendReconciles(t *testing.T) {
	c := newAlertTestCluster(t)
	c.Conf.AlertAlertmanager = true
	c.Conf.AlertAlertmanagerURL = "http://unused.invalid"
	ch := c.AlertChannel(ConstAlertChannelAlertmanager).(*alertmanagerAlertChannel)

	// a trigger state open since before the start: never got an opened edge
	c.StateMachine.AddState("WARN0023", state.State{ErrType: "WARN", ErrDesc: "No GTID", ServerUrl: "db2:3306"})
	out := ch.reconcile(time.Now())
	if len(out) != 1 || out[0].Labels["code"] != "WARN0023" || out[0].Labels["severity"] != "warning" || out[0].EndsAt != "" {
		t.Fatalf("reconcile must fire the open trigger state: %+v", out)
	}
	// the state clears (its resolve missed): one round still firing, the second resolves
	c.StateMachine.ClearState()
	c.StateMachine.ClearState()
	if out = ch.reconcile(time.Now()); len(out) != 1 || out[0].EndsAt != "" {
		t.Fatalf("first miss keeps it firing: %+v", out)
	}
	if out = ch.reconcile(time.Now()); len(out) != 1 || out[0].EndsAt == "" {
		t.Fatalf("second miss resolves it: %+v", out)
	}
	if ch.firingCount() != 0 {
		t.Fatal("the resolved entry must leave the set")
	}

	// the interval: due once, then not before it elapses
	c.Conf.AlertAlertmanagerResendInterval = 60
	now := time.Now()
	if !ch.ResendDue(now) || ch.ResendDue(now.Add(30*time.Second)) || !ch.ResendDue(now.Add(61*time.Second)) {
		t.Fatal("resend interval")
	}
}

// The off-switches: a channel sends only when switched on and configured (T14).
func TestAlertChannelOffSwitch(t *testing.T) {
	c := newAlertTestCluster(t)
	am := c.AlertChannel(ConstAlertChannelAlertmanager)
	tg := c.AlertChannel(ConstAlertChannelTelegram)
	c.Conf.AlertAlertmanagerURL = "http://am:9093"
	c.Conf.AlertTelegramBotToken, c.Conf.AlertTelegramChatID = "1:x", "42"
	if am.Enabled() || tg.Enabled() {
		t.Fatal("configured but switched off must not send")
	}
	c.Conf.AlertAlertmanager, c.Conf.AlertTelegram = true, true
	if !am.Enabled() || !tg.Enabled() {
		t.Fatal("switched on and configured must send")
	}
	c.Conf.AlertAlertmanagerURL, c.Conf.AlertTelegramChatID = "", ""
	if am.Enabled() || tg.Enabled() {
		t.Fatal("switched on without a target must not send")
	}
}

// The routing of the chat channels is the one their call sites had before the registry.
func TestChatChannelRouting(t *testing.T) {
	c := newAlertTestCluster(t)
	teams := c.AlertChannel(ConstAlertChannelTeams)
	pushover := c.AlertChannel(ConstAlertChannelPushover)
	mail := c.AlertChannel(ConstAlertChannelMail)
	line := func(level string, maint bool) AlertEvent {
		return AlertEvent{Kind: AlertLogLine, Level: level, Maintenance: maint}
	}
	for _, tc := range []struct {
		ev                     AlertEvent
		teams, pushover, email bool
	}{
		{line("ERROR", false), true, false, false},
		{line("ERROR", true), false, false, false},
		{line("WARN", false), true, false, false},
		{line("ALERT", true), true, true, false},
		{line("ALERTOK", false), true, true, false},
		{line("START", false), true, true, false},
		{line("INFO", false), false, false, false},
		{AlertEvent{Kind: AlertStateOpened}, false, false, true},
		{AlertEvent{Kind: AlertServerStateChange}, false, false, true},
	} {
		if teams.Accepts(tc.ev) != tc.teams || pushover.Accepts(tc.ev) != tc.pushover || mail.Accepts(tc.ev) != tc.email {
			t.Errorf("%+v: teams %v pushover %v mail %v", tc.ev, teams.Accepts(tc.ev), pushover.Accepts(tc.ev), mail.Accepts(tc.ev))
		}
	}
	c.Conf.TeamsAlertState = "ERR,WARN0023"
	if !teams.Accepts(AlertEvent{Kind: AlertStateLine, State: state.State{ErrKey: "ERR00042"}}) ||
		teams.Accepts(AlertEvent{Kind: AlertStateLine, State: state.State{ErrKey: "WARN0001"}}) {
		t.Fatal("Teams forwards the state lines of alert-teams-state")
	}
	// the Slack channel without its hooks (LogSlack nil) accepts nothing, never panics
	if c.AlertChannel(ConstAlertChannelSlack).Accepts(line("ERROR", false)) {
		t.Fatal("no Slack hooks, nothing to send")
	}
}

// Intervention and the alert-disable window suppress the state alerts before the channels.
func TestStateAlertSuppressed(t *testing.T) {
	c := newAlertTestCluster(t)
	am := newFakeAlertmanager(t)
	c.Conf.AlertAlertmanager = true
	c.Conf.AlertAlertmanagerURL = am.URL
	c.IsIntervention = true
	c.CheckAlert(state.State{ErrKey: "ERR00042", ErrType: "ERROR"}, false)
	time.Sleep(100 * time.Millisecond)
	if am.count() != 0 {
		t.Fatal("an intervention must suppress the alert")
	}
}

// funcChannel sends with fn, under timeout.
type funcChannel struct {
	timeout time.Duration
	fn      func(ctx context.Context) error
}

func (f *funcChannel) Name() string                                 { return "func" }
func (f *funcChannel) Enabled() bool                                { return true }
func (f *funcChannel) Accepts(ev AlertEvent) bool                   { return true }
func (f *funcChannel) Timeout() time.Duration                       { return f.timeout }
func (f *funcChannel) Send(ctx context.Context, _ AlertEvent) error { return f.fn(ctx) }

func funcQueue(c *Cluster, ch AlertChannel) *alertQueue {
	q := &alertQueue{channel: ch, wake: make(chan struct{}, 1)}
	c.alertChannels().queues = []*alertQueue{q}
	return q
}

// A panic in a send is a failed send, and the worker goes on with the next events.
func TestAlertChannelPanicKeepsWorker(t *testing.T) {
	c := newAlertTestCluster(t)
	var calls atomic.Int32
	q := funcQueue(c, &funcChannel{timeout: time.Second, fn: func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("bad event")
		}
		return nil
	}})
	c.dispatchAlert(AlertEvent{Kind: AlertLogLine, Level: "ALERT", Message: "a"})
	waitFor(t, func() bool { return q.failures.Load() == 1 })
	if e, _ := q.lastErr.Load().(string); !strings.Contains(e, "panic") {
		t.Fatalf("last error %q", e)
	}
	c.dispatchAlert(AlertEvent{Kind: AlertLogLine, Level: "ALERT", Message: "b"})
	waitFor(t, func() bool { return q.sent.Load() == 1 })
}

// A send that ignores its context fails at its timeout, the worker goes on, and while that
// send still runs the next ones fail at once: one stuck goroutine per channel at most (F2).
func TestAlertChannelIgnoringContextTimesOut(t *testing.T) {
	c := newAlertTestCluster(t)
	release := make(chan struct{})
	var running atomic.Int32
	q := funcQueue(c, &funcChannel{timeout: 100 * time.Millisecond, fn: func(context.Context) error {
		running.Add(1)
		defer running.Add(-1)
		<-release // a mailer with no deadline
		return nil
	}})
	start := time.Now()
	for i := 0; i < 3; i++ {
		c.dispatchAlert(AlertEvent{Kind: AlertLogLine, Level: "ALERT", Message: "x"})
	}
	waitFor(t, func() bool { return q.failures.Load() >= 2 })
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the worker waited %s on a stuck send", d)
	}
	if n := running.Load(); n != 1 {
		t.Fatalf("%d sends running, want 1", n)
	}
	close(release)
	waitFor(t, func() bool { return !q.inFlight.Load() })
	c.dispatchAlert(AlertEvent{Kind: AlertLogLine, Level: "ALERT", Message: "y"})
	waitFor(t, func() bool { return q.sent.Load() >= 1 })
}

// The alert script is killed with its children at its timeout, even when a child keeps the
// output open; a failed script is a failed send.
func TestAlertScriptTimeoutKillsChildren(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	c := newAlertTestCluster(t)
	script := filepath.Join(t.TempDir(), "alert.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0o755)
	c.Conf.AlertScript = script
	c.Conf.AlertScriptTimeout = 1
	ch := newScriptAlertChannel(c)
	ctx, cancel := context.WithTimeout(context.Background(), ch.Timeout())
	defer cancel()
	start := time.Now()
	err := ch.Send(ctx, AlertEvent{Alert: alert.Alert{Cluster: "c", Host: "h", PrevState: "a", State: "b"}})
	if err == nil {
		t.Fatal("a killed script is a failed send")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the script ran %s past a 1 s timeout", d)
	}
}
