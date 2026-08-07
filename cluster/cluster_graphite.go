package cluster

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/state"
)

const (
	// defaultGraphiteMetricsQueueLimit bounds the in-memory metrics queue
	// when Conf.GraphiteMetricsQueueLimit is unset/<=0 — never unbounded.
	defaultGraphiteMetricsQueueLimit = 100000
	// graphiteSustainedDropThreshold is how many consecutive flush cycles
	// must observe drops before WARN0192 is raised. A single trim on an
	// otherwise-healthy sink (e.g. a burst tick) must not page anyone.
	graphiteSustainedDropThreshold = 2
)

type GraphiteFilterList struct {
	Whitelist string `json:"whitelist"`
	Blacklist string `json:"blacklist"`
}

type ClusterGraphite struct {
	cl           *Cluster           `json:"-"`
	gc           *graphite.Graphite `json:"-"`
	UseWhitelist bool
	UseBlacklist bool
	Whitelist    []*regexp.Regexp
	Blacklist    []*regexp.Regexp
	metrics      []graphite.Metric
	lock         sync.Mutex
	flushing     atomic.Bool

	// DroppedMetricsTotal is a monotonic count of metrics dropped by the
	// queue cap (observability). consecutiveDropFlushes/lastDroppedAtFlush
	// are only touched from SendGraphiteMetrics, which is single-flighted
	// via `flushing`, so they need no separate locking. They track whether
	// drops persist across flush cycles, which is what actually indicates
	// the carbon sink is failing, as opposed to a single transient trim.
	DroppedMetricsTotal    atomic.Uint64
	consecutiveDropFlushes int
	lastDroppedAtFlush     uint64
}

func (cluster *Cluster) NewClusterGraphite() {
	cluster.ClusterGraphite = &ClusterGraphite{
		cl:           cluster,
		UseWhitelist: cluster.Conf.GraphiteWhitelist,
		UseBlacklist: cluster.Conf.GraphiteBlacklist,
	}

	/**
	* Check if whitelist.conf not exists
	* When not exists check if graphite embedded is already set
	* If graphite embedded is immutable, set the default whitelist as grafana to prevent missing metrics
	* The next process will be executed by ReloadGraphiteFilterList()
	 */
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name + "/whitelist.conf"); errors.Is(err, os.ErrNotExist) {
		//This will change the default to grafana
		if active, ok := cluster.Conf.ImmuableFlagMap["graphite-embedded"]; ok && active.(bool) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to read file in cluster %s working dir, change template as grafana due to immutable graphite-embedded exists", cluster.Name)
			cluster.Conf.GraphiteWhitelistTemplate = config.ConstGraphiteTemplateGrafana
		}
	}

	cluster.ReloadGraphiteFilterList()
}

func (cluster *Cluster) GetGraphiteWhitelist() []string {
	ls := make([]string, 0)
	for _, r := range cluster.ClusterGraphite.Whitelist {
		ls = append(ls, r.String())
	}

	return ls
}

func (cluster *Cluster) GetGraphiteBlacklist() []string {
	ls := make([]string, 0)
	for _, r := range cluster.ClusterGraphite.Blacklist {
		ls = append(ls, r.String())
	}

	return ls
}

func (cluster *Cluster) GetGraphiteFilterList() GraphiteFilterList {
	return GraphiteFilterList{
		Whitelist: strings.Join(cluster.GetGraphiteWhitelist(), "\n"),
		Blacklist: strings.Join(cluster.GetGraphiteBlacklist(), "\n"),
	}
}

// Wrapper for clustergraphite write file
func (cluster *Cluster) SetGraphiteFilterList(ft string, fl GraphiteFilterList) error {
	return cluster.ClusterGraphite.WriteFromFilterList(ft, fl)
}

// Wrapper for clustergraphite write file
func (cluster *Cluster) ReloadGraphiteFilterList() error {
	err := cluster.ClusterGraphite.PopulateWhitelistRegexp()
	if err != nil {
		return err
	}
	err = cluster.ClusterGraphite.PopulateBlacklistRegexp()
	if err != nil {
		return err
	}

	return nil
}

func (cg *ClusterGraphite) SetGraphiteConnection(gc *graphite.Graphite) {
	cg.gc = gc
}

// QueueLength returns the current number of buffered metrics awaiting
// flush. Read-only observability accessor — the queue itself (cg.metrics)
// is unexported so its bound (enforced by boundMetrics on every mutation)
// can't be silently bypassed by external code.
func (cg *ClusterGraphite) QueueLength() int {
	cg.lock.Lock()
	defer cg.lock.Unlock()
	return len(cg.metrics)
}

func (cg *ClusterGraphite) GetGraphiteConnection() (*graphite.Graphite, error) {
	if cg.gc != nil {
		return cg.gc, nil
	}
	graph, err := graphite.NewGraphite(cg.cl.Conf.GraphiteCarbonHost, cg.cl.Conf.GraphiteCarbonPort)
	if err != nil {
		return nil, err
	}
	cg.SetGraphiteConnection(graph)
	return cg.gc, nil
}

func (cg *ClusterGraphite) AddMetrics(metrics []graphite.Metric) {
	cg.lock.Lock()
	defer cg.lock.Unlock()

	bounded, dropped := cg.boundMetrics(cg.metrics, metrics)
	cg.metrics = bounded
	if dropped > 0 {
		cg.DroppedMetricsTotal.Add(uint64(dropped))
	}
}

func (cg *ClusterGraphite) SendGraphiteMetrics() error {
	if !cg.flushing.CompareAndSwap(false, true) {
		// A flush is already in progress: skip this tick rather than
		// blocking on it. Anything queued in the meantime is picked up by
		// the next scheduled flush, not immediately after the in-flight one
		// finishes (a deliberate latency/safety tradeoff vs. the old
		// lock-serialized behavior).
		return nil
	}
	defer cg.flushing.Store(false)

	// Evaluated once per flush cycle, before the empty-queue check, so a
	// quiet flush still decays consecutiveDropFlushes back toward recovery.
	cg.checkSustainedDrops()

	cg.lock.Lock()
	batch := cg.metrics
	cg.metrics = make([]graphite.Metric, 0)
	cg.lock.Unlock()

	if len(batch) == 0 {
		return nil
	}

	graph, err := cg.GetGraphiteConnection()
	if err != nil {
		cg.requeue(batch)
		return err
	}

	err = graph.Connect() // Reuses the existing connection if still healthy
	if err != nil {
		cg.requeue(batch)
		return err
	}

	err = graph.SendMetrics(batch)
	if err != nil {
		cg.requeue(batch)
		return err
	}

	return nil
}

// requeue puts a batch that failed to send back at the front of the queue,
// ahead of any metrics appended by producers while the send was in flight.
func (cg *ClusterGraphite) requeue(batch []graphite.Metric) {
	cg.lock.Lock()
	defer cg.lock.Unlock()

	bounded, dropped := cg.boundMetrics(batch, cg.metrics)
	cg.metrics = bounded
	if dropped > 0 {
		cg.DroppedMetricsTotal.Add(uint64(dropped))
	}
}

// queueLimit resolves the configured cap, falling back to a hardcoded safe
// default when unset, <= 0, or when cg.cl is nil (as in unit tests that
// construct a bare &ClusterGraphite{}) — never unbounded.
func (cg *ClusterGraphite) queueLimit() int {
	if cg.cl != nil && cg.cl.Conf.GraphiteMetricsQueueLimit > 0 {
		return cg.cl.Conf.GraphiteMetricsQueueLimit
	}
	return defaultGraphiteMetricsQueueLimit
}

// boundMetrics combines existing (older) and incoming (newer) metrics into
// one queue capped at queueLimit(), dropping from the front (oldest first)
// when over. On a trim, the result is a freshly allocated slice so the
// dropped metric strings can actually be garbage-collected rather than
// lingering in a reused backing array. Returns the bounded queue and how
// many metrics were dropped (0 if under the limit).
func (cg *ClusterGraphite) boundMetrics(existing, incoming []graphite.Metric) ([]graphite.Metric, int) {
	limit := cg.queueLimit()
	total := len(existing) + len(incoming)
	if total <= limit {
		return append(existing, incoming...), 0
	}

	dropped := total - limit
	bounded := make([]graphite.Metric, 0, limit)
	if dropped < len(existing) {
		bounded = append(bounded, existing[dropped:]...)
		bounded = append(bounded, incoming...)
	} else {
		bounded = append(bounded, incoming[dropped-len(existing):]...)
	}
	return bounded, dropped
}

// checkSustainedDrops runs once per flush cycle (called only from
// SendGraphiteMetrics, so it's implicitly single-flighted) and raises
// WARN0192 once drops have persisted across graphiteSustainedDropThreshold
// consecutive cycles. A cycle with no new drops resets the counter, and
// since nothing re-raises the state that tick, it lapses on its own per the
// StateMachine's OldState/CurState rotation.
func (cg *ClusterGraphite) checkSustainedDrops() {
	total := cg.DroppedMetricsTotal.Load()
	if total > cg.lastDroppedAtFlush {
		cg.consecutiveDropFlushes++
	} else {
		cg.consecutiveDropFlushes = 0
	}
	cg.lastDroppedAtFlush = total

	if cg.cl != nil && cg.consecutiveDropFlushes >= graphiteSustainedDropThreshold {
		cg.cl.SetState("WARN0192", state.State{
			ErrType: config.LvlWarn,
			ErrDesc: fmt.Sprintf(clusterError["WARN0192"], cg.queueLimit(), cg.consecutiveDropFlushes, total),
			ErrFrom: "GRAPHITE",
		})
	}
}

func (cg *ClusterGraphite) CopyFromShareDir(filtertype string) error {
	conf := cg.cl.Conf
	var fname string
	var err error
	switch filtertype {
	case "whitelist":
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		template := fmt.Sprintf("whitelist.conf.%s", conf.GraphiteWhitelistTemplate)
		var content []byte
		if conf.WithEmbed == "ON" {
			content, err = share.EmbededDbModuleFS.ReadFile(template)
			if err != nil {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to read file in share dir : %s", err.Error())
				return err
			}
		} else {
			content, err = os.ReadFile(conf.ShareDir + "/" + template)
			if err != nil {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to read file in share dir : %s", err.Error())
				return err
			}
		}
		err = os.WriteFile(fname, content, 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to write file in working dir : %s", err.Error())
			return err
		}
	case "blacklist":
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		template := "blacklist.conf.template"
		var content []byte
		if conf.WithEmbed == "ON" {
			content, err = share.EmbededDbModuleFS.ReadFile(template)
			if err != nil {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to read file in share dir : %s", err.Error())
				return err
			}
		} else {
			content, err = os.ReadFile(conf.ShareDir + "/" + template)
			if err != nil {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to read file in share dir : %s", err.Error())
				return err
			}
		}
		err = os.WriteFile(fname, content, 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to write file in working dir : %s", err.Error())
			return err
		}
	}

	return err
}

func (cg *ClusterGraphite) SetWhitelist(val bool) {
	cg.UseWhitelist = val
	cg.cl.Conf.GraphiteWhitelist = val
}

func (cg *ClusterGraphite) SetBlacklist(val bool) {
	cg.UseBlacklist = val
	cg.cl.Conf.GraphiteBlacklist = val
}

func (cg *ClusterGraphite) WriteFromFilterList(srcfunc string, fl GraphiteFilterList) error {
	conf := cg.cl.Conf
	var err error
	var ls string
	var fname string
	switch srcfunc {
	case "whitelist":
		ls = fl.Whitelist
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
	case "blacklist":
		ls = fl.Blacklist
		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
	}

	cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] reading old file : %s", srcfunc, fname)
	var wg sync.WaitGroup
	wg.Add(1)
	var backup = func(wg *sync.WaitGroup) error {
		defer wg.Done()
		content, err := os.ReadFile(fname)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed reading old file content: %s", srcfunc, err.Error())
			return err
		}

		//Backup old list
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] writing to .old file : %s.old", srcfunc, fname)
		err = os.WriteFile(fname+".old", content, 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed writing new file content : %s", srcfunc, err.Error())
			return err
		}

		return nil
	}

	if err = backup(&wg); err != nil {
		return err
	}

	wg.Wait()
	//This will truncate the old file
	cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlInfo, "[%s] writing new content to .conf file : %s", srcfunc, fname)
	err = os.WriteFile(fname, []byte(ls), 0644)
	if err != nil {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[%s] failed writing new file content : %s", srcfunc, err.Error())
		return err
	}

	return nil
}

func (cg *ClusterGraphite) ResetFilterListRegexp() error {
	var err error
	conf := cg.cl.Conf

	switch conf.GraphiteWhitelistTemplate {
	case config.ConstGraphiteTemplateNone:
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		err = os.WriteFile(fname, []byte(""), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to write file in working dir : %s", err.Error())
			return err
		}

		fname = conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		err = os.WriteFile(fname, []byte(".*"), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to write file in working dir : %s", err.Error())
			return err
		}

		cg.SetWhitelist(false)
		cg.SetBlacklist(true)

	case config.ConstGraphiteTemplateMinimal, config.ConstGraphiteTemplateGrafana:
		err = cg.CopyFromShareDir("whitelist")
		if err != nil {
			return err
		}

		err = cg.CopyFromShareDir("blacklist")
		if err != nil {
			return err
		}
		cg.SetWhitelist(true)
		cg.SetBlacklist(false)

	case config.ConstGraphiteTemplateAll:
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		err = os.WriteFile(fname, []byte(".*"), 0644)
		if err != nil {
			cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to write file in working dir : %s", err.Error())
			return err
		}

		err = cg.CopyFromShareDir("whitelist")
		if err != nil {
			return err
		}
		cg.SetWhitelist(true)
		cg.SetBlacklist(false)

	}

	cg.PopulateWhitelistRegexp()
	cg.PopulateBlacklistRegexp()
	return nil
}

func (cg *ClusterGraphite) PopulateWhitelistRegexp() error {
	conf := cg.cl.Conf
	cg.Whitelist = make([]*regexp.Regexp, 0)

	if conf.GraphiteWhitelist {
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/whitelist.conf"
		file, err := os.Open(fname)
		if err != nil {
			//Create file if not exists
			if errors.Is(err, fs.ErrNotExist) {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] config file not found. Copying from shared dir")
				err = cg.CopyFromShareDir("whitelist")
				if err != nil {
					return err
				}
				if file, err = os.Open(fname); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		ln := 1
		for scanner.Scan() {
			val := scanner.Text()
			if !strings.HasPrefix(val, "#") {
				regex, regErr := regexp.Compile(val)
				if regErr != nil {
					cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Whitelist] failed to parse regexp pattern on line %d : %s. Skipping", ln, val)
				} else {
					cg.Whitelist = append(cg.Whitelist, regex)
				}
			}
			ln++
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (cg *ClusterGraphite) PopulateBlacklistRegexp() error {
	conf := cg.cl.Conf
	cg.Blacklist = make([]*regexp.Regexp, 0)

	if conf.GraphiteBlacklist {
		fname := conf.WorkingDir + "/" + cg.cl.Name + "/blacklist.conf"
		file, err := os.Open(fname)
		if err != nil {
			//Copy file if not exists
			if errors.Is(err, fs.ErrNotExist) {
				cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] config file not found. Copying from shared dir")
				err = cg.CopyFromShareDir("blacklist")
				if err != nil {
					return err
				}
				if file, err = os.Open(fname); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		ln := 1
		for scanner.Scan() {
			val := scanner.Text()
			if !strings.HasPrefix(val, "#") {
				regex, regErr := regexp.Compile(val)
				if regErr != nil {
					cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlWarn, "[Blacklist] failed to parse regexp pattern on line %d : %s. Skipping", ln, val)
				} else {
					cg.Blacklist = append(cg.Blacklist, regex)
				}
			}
			ln++
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (cg *ClusterGraphite) MatchWhitelist(m string) bool {
	if len(cg.Whitelist) > 0 {
		for _, v := range cg.Whitelist {
			if v.MatchString(m) {
				return true
			}
		}

	}
	return false
}

func (cg *ClusterGraphite) MatchBlacklist(m string) bool {
	if len(cg.Blacklist) > 0 {
		for _, v := range cg.Blacklist {
			if v.MatchString(m) {
				return true
			}
		}

	}
	return false
}

func (cg *ClusterGraphite) MatchList(m string) bool {
	conf := cg.cl.Conf
	// If listed in blacklist
	if cg.UseBlacklist && cg.MatchBlacklist(m) {
		cg.cl.LogModulePrintf(conf.Verbose, config.ConstLogModGraphite, config.LvlDbg, "Skip metric in blacklist: %s", m)
		return false
	}

	// If listed in whitelist
	if cg.UseWhitelist {
		if cg.MatchWhitelist(m) {
			return true
		} else {
			// Not found in whitelist
			return false
		}
	}

	//Default is to store
	return true
}
