// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// The Cloud18 account state of the SSO owners of API tokens
// (doc/implementation/server/SESSION_CLUSTER_SCOPE.md, "SSO owner check").
//
// The CRM reads the realm (GET /api/identity/active?email=, the instance's settlement
// credential): an account that exists and is disabled is "inactive" and its tokens are
// refused. The request path never asks the CRM (F2): apiTokenSSOOwnerActive answers from
// ssoOwnerCache, which a background loop refreshes every api-token-sso-owner-check-interval
// for the SSO owners of the live tokens of the store, most recently used first, at most
// api-token-sso-owner-check-max-owners of them (T18). A CRM failure keeps the last answer;
// an owner never answered is active (fail-open, as without the check), and both open
// GWARN026 until the CRM answers again.
//
// An email the realm does not know is not refused: an SSO owner may come from an OAuth
// provider other than the Cloud18 realm, or be a GitLab account linked before the realm;
// the check is about accounts the realm blocks, not about accounts it never had.

const (
	ssoOwnerCheckMinInterval     = 30 * time.Second
	ssoOwnerCheckDefaultInterval = 300 * time.Second
	ssoOwnerCheckDefaultMax      = 200
	ssoOwnerCheckTimeout         = 10 * time.Second
	// ssoOwnerCheckKickMin spaces two refreshes asked by a new owner: a burst of new tokens
	// is one refresh, not one per token.
	ssoOwnerCheckKickMin  = 10 * time.Second
	ssoOwnerCheckStateKey = "GWARN026@api-token-sso-owner"
)

// ssoOwnerAnswer is the CRM's last answer about one owner.
type ssoOwnerAnswer struct {
	Exists    bool
	Active    bool
	CheckedAt time.Time
}

// ssoOwnerCache holds the answers, by lowercased email. Never held across a network call.
type ssoOwnerCache struct {
	mu        sync.Mutex
	answers   map[string]ssoOwnerAnswer
	failure   string // why the last refresh left owners without a current answer; "" = all current
	kick      chan struct{}
	startOnce sync.Once
}

// ssoOwnerLookup asks the CRM about one email; tests replace it.
type ssoOwnerLookup func(email string) (ssoOwnerAnswer, error)

// apiTokenSSOOwnerCheckConfigured reports whether the check can run: the switch is on, the
// instance is registered to Cloud18 and holds its settlement credential.
func (repman *ReplicationManager) apiTokenSSOOwnerCheckConfigured() bool {
	c := repman.Conf
	return c.APITokenSSOOwnerCheck && c.Cloud18 && c.Cloud18SettlementKeyID != "" &&
		repman.Conf.GetDecryptedValue("cloud18-settlement-secret") != ""
}

// initAPITokenSSOOwnerCheck wires apiTokenSSOOwnerActive to the cache and starts its
// refresh loop. The switch and the credential are read at each refresh and each answer:
// turning the check off or losing the credential makes every owner active again.
func (repman *ReplicationManager) initAPITokenSSOOwnerCheck() {
	cache := &repman.ssoOwners
	cache.startOnce.Do(func() {
		cache.mu.Lock()
		cache.answers = map[string]ssoOwnerAnswer{}
		cache.kick = make(chan struct{}, 1)
		cache.mu.Unlock()
		repman.apiTokenSSOOwnerActive = repman.ssoOwnerActiveFromCache
		go repman.ssoOwnerCheckLoop(repman.crmIdentityActive)
	})
}

// ssoOwnerActiveFromCache is apiTokenSSOOwnerActive: no I/O, a map read.
func (repman *ReplicationManager) ssoOwnerActiveFromCache(owner string) bool {
	if !repman.apiTokenSSOOwnerCheckConfigured() {
		return true
	}
	cache := &repman.ssoOwners
	key := strings.ToLower(strings.TrimSpace(owner))
	cache.mu.Lock()
	a, ok := cache.answers[key]
	kick := cache.kick
	cache.mu.Unlock()
	if !ok {
		// a new owner (a token issued since the last refresh): ask now, answer active meanwhile
		select {
		case kick <- struct{}{}:
		default:
		}
		return true
	}
	return !a.Exists || a.Active
}

// ssoOwnerCheckLoop refreshes the cache every interval, and sooner when a new owner asks.
func (repman *ReplicationManager) ssoOwnerCheckLoop(lookup ssoOwnerLookup) {
	lastRun := time.Time{}
	for !repman.exit.Load() {
		if time.Since(lastRun) >= ssoOwnerCheckKickMin {
			repman.refreshSSOOwners(lookup)
			lastRun = time.Now()
		}
		wait := time.NewTimer(repman.ssoOwnerCheckInterval())
		exitPoll := time.NewTicker(time.Second)
	waitLoop:
		for {
			select {
			case <-wait.C:
				break waitLoop
			case <-repman.ssoOwners.kick:
				if time.Since(lastRun) < ssoOwnerCheckKickMin {
					// asked again too soon: refresh at the spacing, not per token
					time.Sleep(ssoOwnerCheckKickMin - time.Since(lastRun))
				}
				break waitLoop
			case <-exitPoll.C:
				if repman.exit.Load() {
					break waitLoop
				}
			}
		}
		wait.Stop()
		exitPoll.Stop()
	}
}

func (repman *ReplicationManager) ssoOwnerCheckInterval() time.Duration {
	d := time.Duration(repman.Conf.APITokenSSOOwnerCheckInterval) * time.Second
	if d <= 0 {
		return ssoOwnerCheckDefaultInterval
	}
	return max(d, ssoOwnerCheckMinInterval)
}

// ssoTokenOwners lists the SSO owners of the live tokens of the store, most recently used
// first, at most limit of them; dropped is how many were left out.
func (repman *ReplicationManager) ssoTokenOwners(limit int) (owners []string, dropped int) {
	st := &repman.apiTokens
	lastUse := map[string]time.Time{}
	st.Lock()
	if err := repman.loadAPITokenStoreLocked(); err == nil {
		for _, t := range st.tokens {
			if t.OwnerAuthType != "SSO" || t.IsRevoked() || t.IsExpired() {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(t.User))
			used := t.LastUsedAt
			if used.IsZero() {
				used = t.CreatedAt
			}
			if prev, ok := lastUse[key]; !ok || used.After(prev) {
				lastUse[key] = used
			}
		}
	}
	st.Unlock()
	for o := range lastUse {
		owners = append(owners, o)
	}
	sort.Slice(owners, func(i, j int) bool {
		if !lastUse[owners[i]].Equal(lastUse[owners[j]]) {
			return lastUse[owners[i]].After(lastUse[owners[j]])
		}
		return owners[i] < owners[j]
	})
	if len(owners) > limit {
		dropped = len(owners) - limit
		owners = owners[:limit]
	}
	return owners, dropped
}

// refreshSSOOwners asks the CRM about every owner kept, outside the cache lock, then replaces
// the answers: an owner no longer owning a live token is dropped, a failed question keeps
// the previous answer.
func (repman *ReplicationManager) refreshSSOOwners(lookup ssoOwnerLookup) {
	cache := &repman.ssoOwners
	if !repman.apiTokenSSOOwnerCheckConfigured() {
		cache.mu.Lock()
		cache.answers = map[string]ssoOwnerAnswer{}
		cache.failure = ""
		cache.mu.Unlock()
		return
	}
	limit := repman.Conf.APITokenSSOOwnerCheckMaxOwners
	if limit <= 0 {
		limit = ssoOwnerCheckDefaultMax
	}
	owners, dropped := repman.ssoTokenOwners(limit)

	cache.mu.Lock()
	previous := cache.answers
	cache.mu.Unlock()

	next := make(map[string]ssoOwnerAnswer, len(owners))
	failed := 0
	lastErr := ""
	for _, o := range owners {
		if repman.exit.Load() {
			return
		}
		a, err := lookup(o)
		if err != nil {
			failed++
			lastErr = err.Error()
			if prev, ok := previous[o]; ok {
				next[o] = prev
			}
			continue
		}
		if a.Exists && !a.Active {
			if prev, ok := previous[o]; !ok || prev.Active || !prev.Exists {
				repman.logSecurityEvent("api_token_owner_inactive", o, "", "Cloud18 account disabled: its API tokens are refused")
			}
		}
		next[o] = a
	}

	var why []string
	if failed > 0 {
		why = append(why, fmt.Sprintf("the CRM did not answer for %d of %d owners (%s)", failed, len(owners), lastErr))
	}
	if dropped > 0 {
		why = append(why, fmt.Sprintf("%d owners above api-token-sso-owner-check-max-owners are not checked", dropped))
	}
	cache.mu.Lock()
	cache.answers = next
	cache.failure = strings.Join(why, "; ")
	cache.mu.Unlock()
}

// ProduceAPITokenSSOOwnerCheckState keeps GWARN026 open while owners lack a current answer.
func (repman *ReplicationManager) ProduceAPITokenSSOOwnerCheckState() {
	cache := &repman.ssoOwners
	cache.mu.Lock()
	failure := cache.failure
	cache.mu.Unlock()
	if failure == "" || !repman.apiTokenSSOOwnerCheckConfigured() {
		return
	}
	repman.SetState(ssoOwnerCheckStateKey, state.State{ErrType: "WARNING", ErrKey: "GWARN026",
		ErrDesc: fmt.Sprintf(config.GlobalError["GWARN026"], failure), ErrFrom: "REPMAN"})
}

// crmIdentityActive asks the CRM whether the Cloud18 account of email exists and is enabled,
// authenticated by the instance's settlement credential. The secret goes in the header only.
func (repman *ReplicationManager) crmIdentityActive(email string) (ssoOwnerAnswer, error) {
	req, err := http.NewRequest(http.MethodGet, repman.crmBase()+"/api/identity/active?email="+url.QueryEscape(email), nil)
	if err != nil {
		return ssoOwnerAnswer{}, err
	}
	req.Header.Set("Authorization", "Bearer "+repman.Conf.GetDecryptedValue("cloud18-settlement-secret"))
	req.Header.Set("X-Settlement-Key-Id", repman.Conf.Cloud18SettlementKeyID)
	req.Header.Set("Accept", "application/json")
	resp, err := ssoOwnerCheckHTTPClient.Do(req)
	if err != nil {
		return ssoOwnerAnswer{}, fmt.Errorf("CRM unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return ssoOwnerAnswer{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ssoOwnerAnswer{}, fmt.Errorf("CRM answered %d", resp.StatusCode)
	}
	var out struct {
		Exists *bool `json:"exists"`
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Active == nil {
		return ssoOwnerAnswer{}, fmt.Errorf("CRM answer unreadable")
	}
	exists := *out.Active
	if out.Exists != nil {
		exists = *out.Exists
	}
	return ssoOwnerAnswer{Exists: exists, Active: *out.Active, CheckedAt: time.Now()}, nil
}

var ssoOwnerCheckHTTPClient = &http.Client{Timeout: ssoOwnerCheckTimeout}
