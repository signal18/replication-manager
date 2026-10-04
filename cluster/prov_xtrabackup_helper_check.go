package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/signal18/replication-manager/config"
)

// The helper image of the xtrabackup injection must not keep a database from starting. On Kubernetes an init
// container that cannot pull its image leaves the pod in Init:ImagePullBackOff, and the database never starts; the
// init container cannot be made optional there. So the render asks the registry first and adds nothing unless the
// registry hands the image out: the database then starts without the injection and the reason is logged.
//
// The question is a manifest HEAD request, bounded by xtrabackupHelperTimeout, made by the provisioning renders and
// never by the monitoring loop. The answers:
//   - the registry hands the image out: rendered;
//   - the registry answers 401, 403 or 404 (a typo, a tag or repository that is gone, one that needs credentials,
//     which Docker Hub also reports as 401) or the reference cannot be parsed: not rendered;
//   - an error of the network (timeout, DNS, connection refused, a 5xx): the registry could not be asked. Rendered only
//     when this process confirmed the image earlier and the registry has not refused it since (a cluster that runs
//     with the injection keeps it through an outage of the registry); otherwise not rendered.
//
// What was confirmed, and every answer kept, belongs to one cluster and one image: another cluster may reach another
// registry or run on other workers, so neither is inherited from a cluster that happened to be confirmed. (A cluster
// moved to workers with another route to the registry is not seen either.)
//
// Every answer is kept for xtrabackupHelperTTL, so a render, which asks several times, costs one request, and a tag
// that disappears, a registry that revokes access or one that comes back is noticed within that time. A confirmation is
// kept for xtrabackupHelperConfirmTTL after the last time the registry handed the image out. Requests for one image are
// shared (one in flight), and no lock is held while the network is used.
//
// The memory is bounded (F4): one entry per cluster and image, an entry whose answer and confirmation have both expired
// is dropped, and the table never holds more than xtrabackupHelperMaxEntries (the least recently used go first), however
// many clusters come and go or however often the setting changes. The requests in flight are bounded too: no more than
// xtrabackupHelperMaxInflight at a time, through that many slots. A caller that finds them all taken waits for its turn,
// no longer than a request takes, and no more than xtrabackupHelperMaxWaiting callers wait, those that wait for a slot and those that wait for the answer of a request
// already out for the same cluster and image alike: a caller that finds the queue full is not queued. When no turn comes, in time or at all, the registry could not be asked for it, and it is
// answered as in that case (rendered only when the cluster confirmed the image earlier), without keeping that answer,
// so it asks again at its next render.
//
// What this does not see: a node that cannot pull an image the registry hands out (a network rule between the node and
// the registry, an image the node cannot reach by itself). That case still ends in Init:ImagePullBackOff on Kubernetes.
const (
	xtrabackupHelperTimeout     = 10 * time.Second
	xtrabackupHelperTTL         = 5 * time.Minute
	xtrabackupHelperConfirmTTL  = 24 * time.Hour
	xtrabackupHelperMaxEntries  = 256
	xtrabackupHelperMaxInflight = 8
	xtrabackupHelperMaxWaiting  = 32
)

// xtrabackupHelperSlotWait is how long a caller waits for a turn when all the slots are taken: no longer than a
// request takes. A variable so that the tests do not wait for it.
var xtrabackupHelperSlotWait = xtrabackupHelperTimeout

// registryHeadCheck asks the registry for the manifest of image.
func registryHeadCheck(ctx context.Context, image string) error {
	_, err := crane.Head(image, crane.WithContext(ctx))
	return err
}

// xtrabackupHelperCheck is the question put to the registry, a variable so that the tests do not reach one.
var xtrabackupHelperCheck = registryHeadCheck

// xtrabackupHelperEntry is what is known of one image for one cluster.
type xtrabackupHelperEntry struct {
	pullable       bool      // the last answer
	until          time.Time // the last answer is used until then
	confirmedUntil time.Time // the registry handed the image out, and has not refused it since, until then
	touched        time.Time // last use, for the bound on the number of entries
}

// xtrabackupHelperFlight is a request in flight, shared by the callers that ask for the same image meanwhile.
type xtrabackupHelperFlight struct {
	done     chan struct{}
	pullable bool
}

var (
	xtrabackupHelperMu       sync.Mutex // guards the two maps below, never held while the network is used
	xtrabackupHelperSlots    = make(chan struct{}, xtrabackupHelperMaxInflight)
	xtrabackupHelperEntries  = map[string]*xtrabackupHelperEntry{}
	xtrabackupHelperInflight = map[string]*xtrabackupHelperFlight{}
	xtrabackupHelperWaiting  int // callers waiting for a slot, at most xtrabackupHelperMaxWaiting, guarded by xtrabackupHelperMu
)

// xtrabackupHelperPruneLocked drops the entries whose answer and confirmation have both expired, then the least
// recently used ones while there are more than xtrabackupHelperMaxEntries. The caller holds xtrabackupHelperMu.
func xtrabackupHelperPruneLocked(now time.Time) {
	for key, e := range xtrabackupHelperEntries {
		if now.After(e.until) && now.After(e.confirmedUntil) {
			delete(xtrabackupHelperEntries, key)
		}
	}
	for len(xtrabackupHelperEntries) > xtrabackupHelperMaxEntries {
		oldestKey, oldest := "", now
		for key, e := range xtrabackupHelperEntries {
			if oldestKey == "" || e.touched.Before(oldest) {
				oldestKey, oldest = key, e.touched
			}
		}
		delete(xtrabackupHelperEntries, oldestKey)
	}
}

// xtrabackupHelperRefused tells whether an error of the check is the registry (or the reference) saying the image
// cannot be pulled, as opposed to a failure of the network.
func xtrabackupHelperRefused(err error) bool {
	var parse *name.ErrBadName
	if errors.As(err, &parse) {
		return true
	}
	var registry *transport.Error
	if errors.As(err, &registry) {
		switch registry.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return true
		}
	}
	return false
}

// xtrabackupHelperPullable reports whether the helper image may be rendered (see the top of this file).
func (cluster *Cluster) xtrabackupHelperPullable(image string) bool {
	key := cluster.Name + "\x00" + image
	// one timer for all the waiting of this call, created when it first has to wait
	var turn *time.Timer
	defer func() {
		if turn != nil {
			turn.Stop()
		}
	}()
	turnC := func() <-chan time.Time {
		if turn == nil {
			turn = time.NewTimer(xtrabackupHelperSlotWait)
		}
		return turn.C
	}
	haveSlot := false
	releaseSlot := func() {
		if haveSlot {
			<-xtrabackupHelperSlots
			haveSlot = false
		}
	}
	defer releaseSlot()

	// giveUp answers a caller that could not be asked for: as when the registry cannot be reached, rendered only if the
	// cluster confirmed the image earlier, and nothing is kept for it (it asks again at its next render)
	giveUp := func(why string, wasConfirmed bool) bool {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"%s for the xtrabackup helper image %s: the injection is rendered only if it was confirmed earlier (%v); it is asked again at the next render", why, image, wasConfirmed)
		return wasConfirmed
	}

	var flight *xtrabackupHelperFlight
	var confirmedUntil time.Time
	var wasConfirmed bool
	for flight == nil {
		xtrabackupHelperMu.Lock()
		if e, ok := xtrabackupHelperEntries[key]; ok && time.Now().Before(e.until) {
			e.touched = time.Now()
			xtrabackupHelperMu.Unlock()
			return e.pullable
		}
		confirmedUntil = time.Time{}
		if e, ok := xtrabackupHelperEntries[key]; ok {
			confirmedUntil = e.confirmedUntil
		}
		wasConfirmed = time.Now().Before(confirmedUntil)

		if shared, ok := xtrabackupHelperInflight[key]; ok {
			// the same cluster and image is being asked for: wait for that answer, as one of the bounded waiters
			if xtrabackupHelperWaiting >= xtrabackupHelperMaxWaiting {
				xtrabackupHelperMu.Unlock()
				return giveUp(fmt.Sprintf("The queue to ask the registry is full (%d waiting)", xtrabackupHelperMaxWaiting), wasConfirmed)
			}
			xtrabackupHelperWaiting++
			xtrabackupHelperMu.Unlock()
			releaseSlot()
			answered := false
			select {
			case <-shared.done:
				answered = true
			case <-turnC():
			}
			xtrabackupHelperMu.Lock()
			xtrabackupHelperWaiting--
			xtrabackupHelperMu.Unlock()
			if answered {
				return shared.pullable
			}
			return giveUp(fmt.Sprintf("No answer from the registry within %s", xtrabackupHelperSlotWait), wasConfirmed)
		}

		if haveSlot {
			// a slot is held: this caller is one of the at most xtrabackupHelperMaxInflight that reach the registry
			flight = &xtrabackupHelperFlight{done: make(chan struct{})}
			xtrabackupHelperInflight[key] = flight
			xtrabackupHelperMu.Unlock()
			break
		}
		if xtrabackupHelperWaiting >= xtrabackupHelperMaxWaiting {
			xtrabackupHelperMu.Unlock()
			return giveUp(fmt.Sprintf("The queue to ask the registry is full (%d waiting)", xtrabackupHelperMaxWaiting), wasConfirmed)
		}
		xtrabackupHelperWaiting++
		xtrabackupHelperMu.Unlock()

		// all the slots are taken: wait for a turn, for no longer than a request takes (F4: the requests, the flights
		// and the callers waiting all stay within their bound)
		gotSlot := false
		select {
		case xtrabackupHelperSlots <- struct{}{}:
			gotSlot = true
		case <-turnC():
		}
		xtrabackupHelperMu.Lock()
		xtrabackupHelperWaiting--
		xtrabackupHelperMu.Unlock()
		if !gotSlot {
			return giveUp(fmt.Sprintf("No turn to ask the registry within %s (%d requests in flight)", xtrabackupHelperSlotWait, xtrabackupHelperMaxInflight), wasConfirmed)
		}
		haveSlot = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), xtrabackupHelperTimeout)
	err := xtrabackupHelperCheck(ctx, image)
	cancel()

	pullable := false
	switch {
	case err == nil:
		pullable = true
		confirmedUntil = time.Now().Add(xtrabackupHelperConfirmTTL)
	case xtrabackupHelperRefused(err):
		confirmedUntil = time.Time{}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"The xtrabackup helper image %s cannot be pulled (%s): the injection is not rendered and the database starts without it. Correct prov-db-docker-xtrabackup-img", image, err)
	case wasConfirmed:
		pullable = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot reach the registry of the xtrabackup helper image %s (%s): it was confirmed earlier, the injection is rendered as configured", image, err)
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Cannot reach the registry of the xtrabackup helper image %s (%s) and it was not confirmed before: the injection is not rendered, so that a helper that cannot be pulled never keeps the database from starting. It is asked again later", image, err)
	}

	xtrabackupHelperMu.Lock()
	now := time.Now()
	xtrabackupHelperEntries[key] = &xtrabackupHelperEntry{pullable: pullable, until: now.Add(xtrabackupHelperTTL), confirmedUntil: confirmedUntil, touched: now}
	xtrabackupHelperPruneLocked(now)
	delete(xtrabackupHelperInflight, key)
	flight.pullable = pullable
	xtrabackupHelperMu.Unlock()
	close(flight.done)
	return pullable
}
