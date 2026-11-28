// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/alertmanager"
	"github.com/signal18/replication-manager/utils/state"
)

// alertmanagerAlertChannel posts the states of monitoring-alert-trigger to Prometheus
// Alertmanager (v2 API). Alertmanager wants a firing alert posted again before its
// resolve_timeout and a cleared one posted with endsAt, so the channel keeps the firing set:
//   - a state that opens is posted at once and joins the set;
//   - a state that clears is posted at once with endsAt and leaves the set;
//   - every alert-alertmanager-resend-interval the set is posted again in one request, after
//     a reconciliation with the state machine (F5): a trigger state open there and missing
//     from the set (a restart, an alert suppressed by an intervention that ended) joins it,
//     and an entry the state machine no longer holds for two rounds in a row is resolved
//     (a resolve missed while alerts were suppressed).
//
// The rate is bounded: one post per state transition (already flap-free, T5) plus one per
// interval. The set is bounded by alert-queue-limit, the oldest dropped first.
type alertmanagerAlertChannel struct {
	cluster *Cluster

	mu         sync.Mutex
	firing     map[string]*firingAlert // by state key (BuildStateKey)
	lastResend time.Time
}

type firingAlert struct {
	alert  alertmanager.Alert
	since  time.Time
	misses int // resend rounds the state machine did not hold the state
}

func newAlertmanagerAlertChannel(cluster *Cluster) *alertmanagerAlertChannel {
	return &alertmanagerAlertChannel{cluster: cluster, firing: map[string]*firingAlert{}}
}

func (c *alertmanagerAlertChannel) Name() string { return ConstAlertChannelAlertmanager }

func (c *alertmanagerAlertChannel) Enabled() bool {
	conf := c.cluster.Conf
	return conf.AlertAlertmanager && strings.TrimSpace(conf.AlertAlertmanagerURL) != ""
}

func (c *alertmanagerAlertChannel) Accepts(ev AlertEvent) bool {
	return ev.Kind == AlertStateOpened || ev.Kind == AlertStateResolved
}

func (c *alertmanagerAlertChannel) Timeout() time.Duration {
	if t := c.cluster.Conf.AlertAlertmanagerTimeout; t > 0 {
		return time.Duration(t) * time.Second
	}
	return 10 * time.Second
}

func (c *alertmanagerAlertChannel) resendInterval() time.Duration {
	if t := c.cluster.Conf.AlertAlertmanagerResendInterval; t > 0 {
		return time.Duration(t) * time.Second
	}
	return 60 * time.Second
}

// ResendDue is true once per resend interval (alertResender).
func (c *alertmanagerAlertChannel) ResendDue(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastResend) < c.resendInterval() {
		return false
	}
	c.lastResend = now
	return true
}

func (c *alertmanagerAlertChannel) client() *alertmanager.Client {
	conf := c.cluster.Conf
	return alertmanager.New(conf.AlertAlertmanagerURL, conf.AlertAlertmanagerUser,
		conf.GetDecryptedValue("alert-alertmanager-password"), conf.GetDecryptedValue("alert-alertmanager-bearer-token"),
		conf.AlertAlertmanagerTLSSkipVerify, c.Timeout())
}

// severity maps a state's type to the Alertmanager convention. Most states carry the type
// "WARNING" (not config.LvlWarn): both are a warning.
func alertSeverity(errType string) string {
	switch errType {
	case config.LvlErr, "ERR":
		return "critical"
	case config.LvlWarn, "WARNING":
		return "warning"
	}
	return "info"
}

// toAlertmanager is the alert of a state: its labels identify it (the same labels from the
// active and the standby monitor, so Alertmanager deduplicates), its annotations say it.
func (c *alertmanagerAlertChannel) toAlertmanager(st state.State, domain string, startsAt time.Time) alertmanager.Alert {
	cluster := c.cluster
	if domain == "" {
		domain = ConstAlertDomainHA
	}
	labels := map[string]string{
		"alertname": st.ErrKey,
		"code":      st.ErrKey,
		"cluster":   cluster.Name,
		"severity":  alertSeverity(st.ErrType),
		"domain":    domain,
		"source":    "replication-manager",
	}
	if st.ServerUrl != "" {
		labels["server"] = st.ServerUrl
	}
	summary := st.ErrDesc
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		summary = summary[:i]
	}
	a := alertmanager.Alert{
		Labels: labels,
		Annotations: map[string]string{
			"summary":     summary,
			"description": st.ErrDesc,
			"monitor":     cluster.GetInstanceAddress(),
		},
		StartsAt: alertmanager.Timestamp(startsAt),
	}
	if u := cluster.Conf.APIPublicURL; strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		a.GeneratorURL = u
	}
	return a
}

func (c *alertmanagerAlertChannel) limit() int {
	if l := c.cluster.Conf.AlertQueueLimit; l > 0 {
		return l
	}
	return defaultAlertQueueLimit
}

// addFiring puts a into the set, dropping the oldest entries over the limit. Caller holds mu.
func (c *alertmanagerAlertChannel) addFiring(key string, a alertmanager.Alert, since time.Time) {
	c.firing[key] = &firingAlert{alert: a, since: since}
	for len(c.firing) > c.limit() {
		oldest := ""
		var at time.Time
		for k, f := range c.firing {
			if oldest == "" || f.since.Before(at) {
				oldest, at = k, f.since
			}
		}
		delete(c.firing, oldest)
	}
}

func (c *alertmanagerAlertChannel) Send(ctx context.Context, ev AlertEvent) error {
	now := ev.Time
	if now.IsZero() {
		now = time.Now()
	}
	switch ev.Kind {
	case AlertStateOpened:
		key := state.BuildStateKey(ev.State.ErrKey, ev.State.ServerUrl)
		a := c.toAlertmanager(ev.State, ev.Domain, now)
		c.mu.Lock()
		c.addFiring(key, a, now)
		c.mu.Unlock()
		return c.client().Post(ctx, []alertmanager.Alert{a})
	case AlertStateResolved:
		key := state.BuildStateKey(ev.State.ErrKey, ev.State.ServerUrl)
		c.mu.Lock()
		a := c.toAlertmanager(ev.State, ev.Domain, now)
		if f, ok := c.firing[key]; ok {
			a.StartsAt = f.alert.StartsAt
			delete(c.firing, key)
		}
		c.mu.Unlock()
		a.EndsAt = alertmanager.Timestamp(now)
		return c.client().Post(ctx, []alertmanager.Alert{a})
	case alertResend:
		alerts := c.reconcile(now)
		if len(alerts) == 0 {
			return nil
		}
		return c.client().Post(ctx, alerts)
	}
	return nil
}

// reconcile brings the firing set in line with the state machine and returns what to post:
// every firing alert, and the resolve of the entries the state machine dropped.
func (c *alertmanagerAlertChannel) reconcile(now time.Time) []alertmanager.Alert {
	cluster := c.cluster
	SM := cluster.GetStateMachine()
	suppressed := cluster.IsIntervention || cluster.IsAlertDisable
	open := map[string]state.State{}
	if SM != nil {
		// the last completed tick: the resend is queued by CheckAlertChannels right after
		// ClearState, when CurState (GetOpenStates) is empty
		for _, st := range SM.GetLastTickStates() {
			if cluster.inAlertTrigger(st.ErrKey) {
				open[state.BuildStateKey(st.ErrKey, st.ServerUrl)] = st
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !suppressed {
		for key, st := range open {
			if _, ok := c.firing[key]; !ok {
				c.addFiring(key, c.toAlertmanager(st, ConstAlertDomainHA, now), now)
			}
		}
	}
	var out []alertmanager.Alert
	keys := make([]string, 0, len(c.firing))
	for k := range c.firing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		f := c.firing[key]
		held := false
		if _, ok := open[key]; ok {
			held = true
		} else if SM != nil && SM.IsInState(key) {
			held = true
		}
		if held {
			f.misses = 0
			out = append(out, f.alert)
			continue
		}
		f.misses++
		if f.misses < 2 {
			out = append(out, f.alert)
			continue
		}
		resolved := f.alert
		resolved.EndsAt = alertmanager.Timestamp(now)
		out = append(out, resolved)
		delete(c.firing, key)
	}
	return out
}

// firingCount is the size of the firing set (tests).
func (c *alertmanagerAlertChannel) firingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.firing)
}
