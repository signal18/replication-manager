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

// defaultGraphiteMetricsMaxQueueSize bounds the in-memory graphite send queue when
// no (or an invalid) value is configured. The queue is a telemetry buffer: when
// the carbon sink cannot keep up it must drop, never grow without bound (a monitor
// that OOMs on a failing sink stops monitoring). See doc DEVELOPMENT_LAWS T18.
const defaultGraphiteMetricsMaxQueueSize = 100000

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
	// DroppedMetrics is the cumulative count of metrics dropped because the send
	// queue hit its cap (carbon sink not draining). Atomic; read for observability
	// and to drive the WARN0209 drop-state.
	DroppedMetrics uint64
	// lastDropReported tracks the drop count already surfaced as a state, so the
	// flush only (re)raises WARN0209 when new drops occurred. Only touched inside
	// the single-flight flush, so it needs no separate lock.
	lastDropReported uint64
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

// maxMetrics returns the configured send-queue cap, falling back to the default
// when unset/invalid. It is never zero — the queue is always bounded (T18).
func (cg *ClusterGraphite) maxMetrics() int {
	if cg.cl != nil && cg.cl.Conf.GraphiteMetricsMaxQueueSize > 0 {
		return cg.cl.Conf.GraphiteMetricsMaxQueueSize
	}
	return defaultGraphiteMetricsMaxQueueSize
}

// capMetricsLocked enforces the send-queue cap; cg.lock must be held. When the
// queue exceeds the cap it drops the OLDEST (front) metrics — the stalest data —
// copying the newest ones into a fresh backing array so the dropped metrics and
// their strings are actually released (a forward reslice would keep the old
// backing array, and its strings, alive). Returns the number dropped.
func (cg *ClusterGraphite) capMetricsLocked() int {
	over := len(cg.metrics) - cg.maxMetrics()
	if over <= 0 {
		return 0
	}
	kept := make([]graphite.Metric, cg.maxMetrics())
	copy(kept, cg.metrics[over:])
	cg.metrics = kept
	atomic.AddUint64(&cg.DroppedMetrics, uint64(over))
	return over
}

// reportMetricDrops raises WARN0209 when new drops have occurred since the last
// flush. Called once per flush (single-flight, every 5 ticks); on the 4
// intervening ticks WARN0209 is held by the %5 PreserveState block in the
// monitor loop, so the alert reflects sustained drops without flapping.
func (cg *ClusterGraphite) reportMetricDrops() {
	if cg.cl == nil {
		return
	}
	dropped := atomic.LoadUint64(&cg.DroppedMetrics)
	if dropped <= cg.lastDropReported {
		return
	}
	delta := dropped - cg.lastDropReported
	cg.lastDropReported = dropped
	cg.cl.SetState("WARN0209", state.State{
		ErrType: "WARNING",
		ErrDesc: fmt.Sprintf(clusterError["WARN0209"], delta, dropped),
		ErrFrom: "MON",
	})
}

func (cg *ClusterGraphite) AddMetrics(metrics []graphite.Metric) {
	cg.lock.Lock()
	defer cg.lock.Unlock()

	cg.metrics = append(cg.metrics, metrics...)
	cg.capMetricsLocked()
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

	// Surface sustained queue drops (carbon sink not draining) as WARN0209.
	cg.reportMetricDrops()

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

	err = graph.Connect() // Ensure connection is established
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
// ahead of any metrics appended by producers while the send was in flight. It is
// bounded: if the requeued batch + newer metrics exceed the cap, the oldest are
// dropped (T18) — otherwise a persistently-failing sink would grow the queue
// forever via requeue.
func (cg *ClusterGraphite) requeue(batch []graphite.Metric) {
	cg.lock.Lock()
	defer cg.lock.Unlock()
	cg.metrics = append(batch, cg.metrics...)
	cg.capMetricsLocked()
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
