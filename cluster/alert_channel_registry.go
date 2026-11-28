// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// defaultAlertQueueLimit is the per-channel queue cap when alert-queue-limit is unset or <= 0:
// never unbounded (T18).
const defaultAlertQueueLimit = 1000

// alertDropStateHold is how long WARN0362 stays open after a channel's last drop, so a burst
// is seen in the GUI and the state does not flap with each alert (T5).
const alertDropStateHold = 5 * time.Minute

// alertFailureThreshold is the number of consecutive failed sends of a channel that opens
// WARN0363.
const alertFailureThreshold = 3

// alertResend is the internal event a channel with a resend cycle (alertResender) gets on its
// own queue: the alertmanager channel posts its firing set again.
const alertResend AlertEventKind = -1

// alertResender is a channel that must post again what is still firing, at its own interval
// (Alertmanager resolves an alert it has not heard of within its resolve_timeout).
type alertResender interface {
	ResendDue(now time.Time) bool
}

// alertQueue is one channel's bounded queue and its worker's bookkeeping.
type alertQueue struct {
	channel AlertChannel

	mu    sync.Mutex
	items []AlertEvent
	wake  chan struct{}
	start sync.Once

	dropped      atomic.Uint64 // monotonic, alerts dropped by the cap
	lastDropUnix atomic.Int64
	failures     atomic.Uint64 // consecutive failed sends; 0 after a success
	inFlight     atomic.Bool   // a send past its timeout still runs: the next one does not start
	lastErr      atomic.Value  // string, the last send error (redacted by the channel)
	sent         atomic.Uint64
}

// push adds ev, dropping the oldest events when over limit. On a drop the slice is copied
// into a fresh array, so the dropped events are freed (T18). Returns how many were dropped.
func (q *alertQueue) push(ev AlertEvent, limit int) int {
	q.mu.Lock()
	q.items = append(q.items, ev)
	dropped := 0
	if n := len(q.items); n > limit {
		dropped = n - limit
		kept := make([]AlertEvent, limit)
		copy(kept, q.items[dropped:])
		q.items = kept
	}
	q.mu.Unlock()
	if dropped > 0 {
		q.dropped.Add(uint64(dropped))
		q.lastDropUnix.Store(time.Now().UnixNano())
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return dropped
}

func (q *alertQueue) pop() (AlertEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return AlertEvent{}, false
	}
	ev := q.items[0]
	q.items[0] = AlertEvent{}
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil // release the backing array
	}
	return ev, true
}

func (q *alertQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// alertChannelRegistry is the cluster's alert channels, each with its queue and worker.
type alertChannelRegistry struct {
	cluster *Cluster
	queues  []*alertQueue // fixed order: the order the channels were called in before

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newAlertChannelList builds the cluster's channels, as newOrchestratorList builds its
// orchestrators. Every channel is registered whatever its settings: Enabled reads them at
// each alert, so a channel turned on through the API sends at the next alert.
func (cluster *Cluster) newAlertChannelList() *alertChannelRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	r := &alertChannelRegistry{cluster: cluster, ctx: ctx, cancel: cancel}
	for _, ch := range []AlertChannel{
		newMailAlertChannel(cluster),
		newScriptAlertChannel(cluster),
		newSlackAlertChannel(cluster),
		newPushoverAlertChannel(cluster),
		newTeamsAlertChannel(cluster),
		newAlertmanagerAlertChannel(cluster),
		newTelegramAlertChannel(cluster),
	} {
		r.queues = append(r.queues, &alertQueue{channel: ch, wake: make(chan struct{}, 1)})
	}
	return r
}

// alertChannels is the cluster's registry, built at first use: a log line can be printed
// before InitFromConf.
func (cluster *Cluster) alertChannels() *alertChannelRegistry {
	cluster.alertChannelsOnce.Do(func() {
		cluster.alertChannelReg = cluster.newAlertChannelList()
	})
	return cluster.alertChannelReg
}

// AlertChannel returns the registered channel of that name, nil if none.
func (cluster *Cluster) AlertChannel(name string) AlertChannel {
	for _, q := range cluster.alertChannels().queues {
		if q.channel.Name() == name {
			return q.channel
		}
	}
	return nil
}

func (r *alertChannelRegistry) queueLimit() int {
	if c := r.cluster.Conf; c != nil && c.AlertQueueLimit > 0 {
		return c.AlertQueueLimit
	}
	return defaultAlertQueueLimit
}

// dispatchAlert hands ev to every enabled channel that accepts it. It only enqueues: the
// sends run on the channels' workers, never on the caller (the monitor tick, a log call).
// It returns the number of channels that took the event.
func (cluster *Cluster) dispatchAlert(ev AlertEvent) int {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	r := cluster.alertChannels()
	n := 0
	for _, q := range r.queues {
		if q.channel.Accepts(ev) && q.channel.Enabled() { // Accepts first: Enabled may cost more (the mail's recipients)
			r.enqueue(q, ev)
			n++
		}
	}
	return n
}

func (r *alertChannelRegistry) enqueue(q *alertQueue, ev AlertEvent) {
	if r.ctx.Err() != nil {
		return // stopped
	}
	q.start.Do(func() {
		r.wg.Add(1)
		go r.work(q)
	})
	q.push(ev, r.queueLimit())
}

// work sends the queue's events one at a time, each bounded by the channel's timeout.
func (r *alertChannelRegistry) work(q *alertQueue) {
	defer r.wg.Done()
	defer r.cluster.LogPanicToFile("alert-channel-" + q.channel.Name())
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-q.wake:
		}
		for {
			ev, ok := q.pop()
			if !ok {
				break
			}
			if r.ctx.Err() != nil {
				return
			}
			r.send(q, ev)
		}
	}
}

// send runs one Send under the channel's timeout, in a goroutine of its own: a channel that
// does not honour its context (the mail, a hook with its own client) or that panics never
// stops its worker. A send past its timeout is counted failed; while it still runs, the
// next events of that channel fail at once instead of piling up goroutines (one at most per
// channel, F2/T18).
func (r *alertChannelRegistry) send(q *alertQueue, ev AlertEvent) {
	timeout := q.channel.Timeout()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var err error
	if q.inFlight.Load() {
		err = errors.New("its previous send is still running past its timeout")
	} else {
		ctx, cancel := context.WithTimeout(r.ctx, timeout)
		defer cancel()
		q.inFlight.Store(true)
		done := make(chan error, 1)
		go func() {
			defer q.inFlight.Store(false)
			defer func() {
				if p := recover(); p != nil {
					done <- fmt.Errorf("panic in the send: %v", p)
				}
			}()
			done <- q.channel.Send(ctx, ev)
		}()
		select {
		case err = <-done:
		case <-ctx.Done():
			select {
			case err = <-done: // it ended with its context
			case <-time.After(alertSendGrace):
				err = fmt.Errorf("no answer after %s", timeout)
			}
		}
	}
	if err == nil {
		q.failures.Store(0)
		q.sent.Add(1)
		return
	}
	n := q.failures.Add(1)
	q.lastErr.Store(err.Error())
	if n <= alertFailureThreshold {
		r.cluster.LogModulePrintf(r.cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not send %s alert: %s", q.channel.Name(), err)
	}
}

// alertSendGrace is how long a send whose context ended may still take to return.
const alertSendGrace = time.Second

// alertStopWait bounds how long a cluster stop waits for a worker in the middle of a send
// that does not honour its context (the mail): the stop never hangs on a channel (F2).
const alertStopWait = 5 * time.Second

// stop ends the workers; the events still queued are not sent.
func (r *alertChannelRegistry) stop() {
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(alertStopWait):
	}
}

// stopAlertChannels ends the alert workers (Cluster.Stop).
func (cluster *Cluster) stopAlertChannels() {
	if cluster.alertChannelReg != nil {
		cluster.alertChannelReg.stop()
	}
}

// AlertChannelStatus is one channel's queue as the API shows it.
type AlertChannelStatus struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Queued   int    `json:"queued"`
	Sent     uint64 `json:"sent"`
	Dropped  uint64 `json:"dropped"`
	Failures uint64 `json:"failures"`
	LastErr  string `json:"lastError,omitempty"`
}

// GetAlertChannelStatus lists the channels with their queue counters.
func (cluster *Cluster) GetAlertChannelStatus() []AlertChannelStatus {
	var out []AlertChannelStatus
	for _, q := range cluster.alertChannels().queues {
		s := AlertChannelStatus{
			Name:     q.channel.Name(),
			Enabled:  q.channel.Enabled(),
			Queued:   q.len(),
			Sent:     q.sent.Load(),
			Dropped:  q.dropped.Load(),
			Failures: q.failures.Load(),
		}
		if v, ok := q.lastErr.Load().(string); ok && s.Failures > 0 {
			s.LastErr = v
		}
		out = append(out, s)
	}
	return out
}

// CheckAlertChannels runs once per monitor tick: it opens WARN0362 while a channel has
// dropped alerts in the last alertDropStateHold, WARN0363 while a channel's last sends
// failed, and queues the resend of the channels that have one (Alertmanager), on the active
// monitor only, as SendAlert sends on the active monitor only.
func (cluster *Cluster) CheckAlertChannels() {
	r := cluster.alertChannels()
	now := time.Now()
	var dropping, failing []string
	for _, q := range r.queues {
		if last := q.lastDropUnix.Load(); last > 0 && now.Sub(time.Unix(0, last)) < alertDropStateHold {
			dropping = append(dropping, fmt.Sprintf("%s (%d dropped)", q.channel.Name(), q.dropped.Load()))
		}
		// a channel switched off sends nothing more: its last failures no longer hold WARN0363
		if q.failures.Load() >= alertFailureThreshold && q.channel.Enabled() {
			msg, _ := q.lastErr.Load().(string)
			failing = append(failing, fmt.Sprintf("%s: %s", q.channel.Name(), msg))
		}
		if res, ok := q.channel.(alertResender); ok && q.channel.Enabled() && res.ResendDue(now) {
			if cluster.Status == ConstMonitorActif || !cluster.IsDiscovered() {
				r.enqueue(q, AlertEvent{Kind: alertResend, Time: now})
			}
		}
	}
	if len(dropping) > 0 {
		sort.Strings(dropping)
		cluster.SetState("WARN0362", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0362"], r.queueLimit(), strings.Join(dropping, ", ")), ErrFrom: "ALERT"})
	}
	if len(failing) > 0 {
		sort.Strings(failing)
		cluster.SetState("WARN0363", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0363"], strings.Join(failing, "; ")), ErrFrom: "ALERT"})
	}
}
