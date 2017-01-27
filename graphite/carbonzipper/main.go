package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"expvar"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	pb "github.com/dgryski/carbonzipper/carbonzipperpb"
	"github.com/dgryski/carbonzipper/mlog"
	"github.com/dgryski/carbonzipper/mstats"
	"github.com/dgryski/go-expirecache"
	"github.com/dgryski/httputil"
	"github.com/facebookgo/grace/gracehttp"
	"github.com/facebookgo/pidfile"
	pickle "github.com/kisielk/og-rek"
	"github.com/peterbourgon/g2g"
)

// Config contains configuration values
var Config = struct {
	Backends    []string
	MaxProcs    int
	IntervalSec int
	Port        int
	Buckets     int

	TimeoutMs                int
	TimeoutMsAfterAllStarted int

	SearchBackend string
	SearchPrefix  string

	GraphiteHost string

	pathCache pathCache

	MaxIdleConnsPerHost int

	ConcurrencyLimitPerServer int
}{
	MaxProcs:    1,
	IntervalSec: 60,
	Port:        8080,
	Buckets:     10,

	TimeoutMs:                10000,
	TimeoutMsAfterAllStarted: 2000,

	MaxIdleConnsPerHost: 100,

	pathCache: pathCache{ec: expirecache.New(0)},
}

// Metrics contains grouped expvars for /debug/vars and graphite
var Metrics = struct {
	FindRequests *expvar.Int
	FindErrors   *expvar.Int

	SearchRequests *expvar.Int

	RenderRequests *expvar.Int
	RenderErrors   *expvar.Int

	InfoRequests *expvar.Int
	InfoErrors   *expvar.Int

	Timeouts *expvar.Int

	CacheSize  expvar.Func
	CacheItems expvar.Func
}{
	FindRequests: expvar.NewInt("find_requests"),
	FindErrors:   expvar.NewInt("find_errors"),

	SearchRequests: expvar.NewInt("search_requests"),

	RenderRequests: expvar.NewInt("render_requests"),
	RenderErrors:   expvar.NewInt("render_errors"),

	InfoRequests: expvar.NewInt("info_requests"),
	InfoErrors:   expvar.NewInt("info_errors"),

	Timeouts: expvar.NewInt("timeouts"),
}

// BuildVersion is defined at build and reported at startup and as expvar
var BuildVersion = "(development version)"

// Limiter limits our concurrency to a particular server
var Limiter serverLimiter

var logger mlog.Level

type serverResponse struct {
	server   string
	response []byte
}

// set during startup, read-only after that
var searchConfigured = false

var storageClient = &http.Client{}

var probeTicker = time.NewTicker(10 * time.Minute)
var probeQuit = make(chan struct{})
var probeForce = make(chan int)

func doProbe() {
	query := "/metrics/find/?format=protobuf&query=%2A"

	responses := multiGet(Config.Backends, query)

	if len(responses) == 0 {
		return
	}

	_, paths := findUnpackPB(nil, responses)

	// update our cache of which servers have which metrics
	for k, v := range paths {
		Config.pathCache.set(k, v)
		logger.Debugln("TLD probe:", k, "servers =", v)
	}
}

func probeTlds() {
	for {
		select {
		case <-probeTicker.C:
			doProbe()
		case <-probeForce:
			doProbe()
		case <-probeQuit:
			probeTicker.Stop()
			return
		}
	}
}

func singleGet(uri, server string, ch chan<- serverResponse, started chan<- struct{}) {

	u, err := url.Parse(server + uri)
	if err != nil {
		logger.Logln("error parsing uri: ", server+uri, ":", err)
		ch <- serverResponse{server, nil}
		return
	}
	req := http.Request{
		URL:    u,
		Header: make(http.Header),
	}

	Limiter.enter(server)
	started <- struct{}{}
	defer Limiter.leave(server)
	resp, err := storageClient.Do(&req)
	if err != nil {
		logger.Logln("singleGet: error querying ", server, "/", uri, ":", err)
		ch <- serverResponse{server, nil}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// carbonsserver replies with Not Found if we request a
		// metric that it doesn't have -- makes sense
		ch <- serverResponse{server, nil}
		return
	}

	if resp.StatusCode != http.StatusOK {
		logger.Logln("bad response code ", server, "/", uri, ":", resp.StatusCode)
		ch <- serverResponse{server, nil}
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Logln("error reading body: ", server, "/", uri, ":", err)
		ch <- serverResponse{server, nil}
		return
	}

	ch <- serverResponse{server, body}
}

func multiGet(servers []string, uri string) []serverResponse {

	logger.Debugln("querying servers=", servers, "uri=", uri)

	// buffered channel so the goroutines don't block on send
	ch := make(chan serverResponse, len(servers))
	startedch := make(chan struct{}, len(servers))

	for _, server := range servers {
		go singleGet(uri, server, ch, startedch)
	}

	var response []serverResponse

	timeout := time.After(time.Duration(Config.TimeoutMs) * time.Millisecond)

	var responses int
	var started int

GATHER:
	for {
		select {
		case <-startedch:
			started++
			if started == len(servers) {
				timeout = time.After(time.Duration(Config.TimeoutMsAfterAllStarted) * time.Millisecond)
			}

		case r := <-ch:
			responses++
			if r.response != nil {
				response = append(response, r)
			}

			if responses == len(servers) {
				break GATHER
			}

		case <-timeout:
			var servs []string
			for _, r := range response {
				servs = append(servs, r.server)
			}
			// TODO(dgryski): log which servers have/haven't started
			logger.Logln("Timeout waiting for more responses.  uri=", uri, ", servers=", servers, ", answers_from_servers=", servs)
			Metrics.Timeouts.Add(1)
			break GATHER
		}
	}

	return response
}

type nameleaf struct {
	name string
	leaf bool
}

func findUnpackPB(req *http.Request, responses []serverResponse) ([]*pb.GlobMatch, map[string][]string) {

	// metric -> [server1, ... ]
	paths := make(map[string][]string)
	seen := make(map[nameleaf]bool)

	var metrics []*pb.GlobMatch
	for _, r := range responses {
		var metric pb.GlobResponse
		err := metric.Unmarshal(r.response)
		if err != nil && req != nil {
			logger.Logf("error decoding protobuf response from server:%s: req:%s: err=%s", r.server, req.URL.RequestURI(), err)
			logger.Traceln("\n" + hex.Dump(r.response))
			Metrics.FindErrors.Add(1)
			continue
		}

		for _, match := range metric.Matches {
			n := nameleaf{*match.Path, *match.IsLeaf}
			_, ok := seen[n]
			if !ok {
				// we haven't seen this name yet
				// add the metric to the list of metrics to return
				metrics = append(metrics, match)
				seen[n] = true
			}
			// add the server to the list of servers that know about this metric
			p := paths[*match.Path]
			p = append(p, r.server)
			paths[*match.Path] = p
		}
	}

	return metrics, paths
}

const (
	contentTypeJSON     = "application/json"
	contentTypeProtobuf = "application/x-protobuf"
	contentTypePickle   = "application/pickle"
)

func findHandler(w http.ResponseWriter, req *http.Request) {

	logger.Debugln("request: ", req.URL.RequestURI())

	Metrics.FindRequests.Add(1)

	originalQuery := req.FormValue("query")
	queries := []string{originalQuery}

	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	format := req.FormValue("format")
	v.Set("format", "protobuf")
	rewrite.RawQuery = v.Encode()

	if searchConfigured && strings.HasPrefix(queries[0], Config.SearchPrefix) {
		Metrics.SearchRequests.Add(1)
		// 'completer' requests are translated into standard Find requests with
		// a trailing '*' by graphite-web
		if strings.HasSuffix(queries[0], "*") {
			searchCompleterResponse := multiGet([]string{Config.SearchBackend}, rewrite.RequestURI())
			matches, _ := findUnpackPB(nil, searchCompleterResponse)
			// this is a completer request, and so we should return the set of
			// virtual metrics returned by carbonsearch verbatim, rather than trying
			// to find them on the stores
			encodeFindResponse(format, originalQuery, w, matches)
			return
		}

		// Send query to SearchBackend. The result is []queries for StorageBackends
		searchResponse := multiGet([]string{Config.SearchBackend}, rewrite.RequestURI())
		m, _ := findUnpackPB(req, searchResponse)
		queries = make([]string, 0, len(m))
		for _, v := range m {
			queries = append(queries, *v.Path)
		}
	}

	var metrics []*pb.GlobMatch
	// TODO(nnuss): Rewrite the result queries to a series of brace expansions based on TLD?
	// [a.b, a.c, a.dee.eee.eff, x.y] => [ "a.{b,c,dee.eee.eff}", "x.y" ]
	// Be mindful that carbonserver's default MaxGlobs is 10
	for _, query := range queries {

		v.Set("query", query)
		rewrite.RawQuery = v.Encode()

		var tld string
		if i := strings.IndexByte(query, '.'); i > 0 {
			tld = query[:i]
		}

		// lookup tld in our map of where they live to reduce the set of
		// servers we bug with our find
		var backends []string
		var ok bool
		if backends, ok = Config.pathCache.get(tld); !ok || backends == nil || len(backends) == 0 {
			backends = Config.Backends
		}

		responses := multiGet(backends, rewrite.RequestURI())

		if len(responses) == 0 {
			logger.Logln("find: error querying backends for: ", rewrite.RequestURI())
			http.Error(w, "find: error querying backends", http.StatusInternalServerError)
			return
		}

		m, paths := findUnpackPB(req, responses)
		metrics = append(metrics, m...)

		// update our cache of which servers have which metrics
		for k, v := range paths {
			Config.pathCache.set(k, v)
		}
	}

	encodeFindResponse(format, originalQuery, w, metrics)
}

func encodeFindResponse(format, query string, w http.ResponseWriter, metrics []*pb.GlobMatch) {
	switch format {
	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb.GlobResponse
		result.Name = &query
		result.Matches = metrics
		b, _ := result.Marshal()
		w.Write(b)
	case "json":
		w.Header().Set("Content-Type", contentTypeJSON)
		jEnc := json.NewEncoder(w)
		jEnc.Encode(metrics)
	case "", "pickle":
		w.Header().Set("Content-Type", contentTypePickle)

		var result []map[string]interface{}

		for _, metric := range metrics {
			mm := map[string]interface{}{
				"metric_path": *metric.Path,
				"isLeaf":      *metric.IsLeaf,
			}
			result = append(result, mm)
		}

		pEnc := pickle.NewEncoder(w)
		pEnc.Encode(result)
	}
}

func renderHandler(w http.ResponseWriter, req *http.Request) {

	logger.Debugln("request: ", req.URL.RequestURI())

	Metrics.RenderRequests.Add(1)

	req.ParseForm()
	target := req.FormValue("target")

	if target == "" {
		http.Error(w, "empty target", http.StatusBadRequest)
		return
	}

	var serverList []string
	var ok bool

	// lookup the server list for this metric, or use all the servers if it's unknown
	if serverList, ok = Config.pathCache.get(target); !ok || serverList == nil || len(serverList) == 0 {
		serverList = Config.Backends
	}

	format := req.FormValue("format")
	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	v.Set("format", "protobuf")
	rewrite.RawQuery = v.Encode()

	responses := multiGet(serverList, rewrite.RequestURI())

	if len(responses) == 0 {
		logger.Logln("render: error querying backends for:", req.URL.RequestURI(), "backends:", serverList)
		http.Error(w, "render: error querying backends", http.StatusInternalServerError)
		Metrics.RenderErrors.Add(1)
		return
	}

	metrics := mergeResponses(req, responses)
	if metrics == nil {
		Metrics.RenderErrors.Add(1)
		err := fmt.Sprintf("no decoded responses to merge for req: %s", req.URL.RequestURI())
		logger.Logln(err)
		http.Error(w, "no decoded responses to merge", http.StatusInternalServerError)
		return
	}

	switch format {
	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		b, _ := metrics.Marshal()
		w.Write(b)

	case "json":
		presponse := createRenderResponse(metrics, nil)
		w.Header().Set("Content-Type", contentTypeJSON)
		e := json.NewEncoder(w)
		e.Encode(presponse)

	case "", "pickle":
		presponse := createRenderResponse(metrics, pickle.None{})
		w.Header().Set("Content-Type", contentTypePickle)
		e := pickle.NewEncoder(w)
		e.Encode(presponse)
	}
}

func createRenderResponse(metrics *pb.MultiFetchResponse, missing interface{}) []map[string]interface{} {

	var response []map[string]interface{}

	for _, metric := range metrics.GetMetrics() {

		var pvalues []interface{}
		for i, v := range metric.Values {
			if metric.IsAbsent[i] {
				pvalues = append(pvalues, missing)
			} else {
				pvalues = append(pvalues, v)
			}
		}

		// create the response
		presponse := map[string]interface{}{
			"start":  metric.StartTime,
			"step":   metric.StepTime,
			"end":    metric.StopTime,
			"name":   metric.Name,
			"values": pvalues,
		}
		response = append(response, presponse)
	}

	return response
}

func mergeResponses(req *http.Request, responses []serverResponse) *pb.MultiFetchResponse {

	metrics := make(map[string][]pb.FetchResponse)

	for _, r := range responses {
		var d pb.MultiFetchResponse
		err := d.Unmarshal(r.response)
		if err != nil {
			logger.Logf("error decoding protobuf response from server:%s: req:%s: err=%s", r.server, req.URL.RequestURI(), err)
			logger.Traceln("\n" + hex.Dump(r.response))
			Metrics.RenderErrors.Add(1)
			continue
		}
		for _, m := range d.Metrics {
			metrics[m.GetName()] = append(metrics[m.GetName()], *m)
		}
	}

	var multi pb.MultiFetchResponse

	if len(metrics) == 0 {
		return nil
	}

	for name, decoded := range metrics {

		logger.Tracef("request: %s: %q %+v", req.URL.RequestURI(), name, decoded)

		if len(decoded) == 1 {
			logger.Debugf("only one decoded responses to merge for req: %q %s", name, req.URL.RequestURI())
			m := decoded[0]
			multi.Metrics = append(multi.Metrics, &m)
			continue
		}

		// Use the metric with the highest resolution as our base
		var highest int
		for i, d := range decoded {
			if d.GetStepTime() < decoded[highest].GetStepTime() {
				highest = i
			}
		}
		decoded[0], decoded[highest] = decoded[highest], decoded[0]

		metric := decoded[0]

		mergeValues(req, &metric, decoded)
		multi.Metrics = append(multi.Metrics, &metric)
	}

	return &multi
}

func mergeValues(req *http.Request, metric *pb.FetchResponse, decoded []pb.FetchResponse) {

	var responseLengthMismatch bool
	for i := range metric.Values {
		if !metric.IsAbsent[i] || responseLengthMismatch {
			continue
		}

		// found a missing value, find a replacement
		for other := 1; other < len(decoded); other++ {

			m := decoded[other]

			if len(m.Values) != len(metric.Values) {
				logger.Logf("request: %s: unable to merge ovalues: len(values)=%d but len(ovalues)=%d", req.URL.RequestURI(), len(metric.Values), len(m.Values))
				// TODO(dgryski): we should remove
				// decoded[other] from the list of responses to
				// consider but this assumes that decoded[0] is
				// the 'highest resolution' response and thus
				// the one we want to keep, instead of the one
				// we want to discard

				Metrics.RenderErrors.Add(1)
				responseLengthMismatch = true
				break
			}

			// found one
			if !m.IsAbsent[i] {
				metric.IsAbsent[i] = false
				metric.Values[i] = m.Values[i]
			}
		}
	}
}

func infoUnpackPB(req *http.Request, format string, responses []serverResponse) map[string]pb.InfoResponse {

	decoded := make(map[string]pb.InfoResponse)
	for _, r := range responses {
		if r.response == nil {
			continue
		}
		var d pb.InfoResponse
		err := d.Unmarshal(r.response)
		if err != nil {
			logger.Logf("error decoding protobuf response from server:%s: req:%s: err=%s", r.server, req.URL.RequestURI(), err)
			logger.Traceln("\n" + hex.Dump(r.response))
			Metrics.InfoErrors.Add(1)
			continue
		}
		decoded[r.server] = d
	}

	logger.Tracef("request: %s: %v", req.URL.RequestURI(), decoded)

	return decoded
}

func infoHandler(w http.ResponseWriter, req *http.Request) {

	logger.Debugln("request: ", req.URL.RequestURI())

	Metrics.InfoRequests.Add(1)

	req.ParseForm()
	target := req.FormValue("target")

	if target == "" {
		http.Error(w, "empty target", http.StatusBadRequest)
		return
	}

	var serverList []string
	var ok bool

	// lookup the server list for this metric, or use all the servers if it's unknown
	if serverList, ok = Config.pathCache.get(target); !ok || serverList == nil || len(serverList) == 0 {
		serverList = Config.Backends
	}

	format := req.FormValue("format")
	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	v.Set("format", "protobuf")
	rewrite.RawQuery = v.Encode()

	responses := multiGet(serverList, rewrite.RequestURI())

	if len(responses) == 0 {
		logger.Logln("info: error querying backends for:", req.URL.RequestURI(), "backends:", serverList)
		http.Error(w, "info: error querying backends", http.StatusInternalServerError)
		Metrics.InfoErrors.Add(1)
		return
	}

	infos := infoUnpackPB(req, format, responses)

	switch format {
	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb.ZipperInfoResponse
		result.Responses = make([]*pb.ServerInfoResponse, len(infos))
		for s, i := range infos {
			var r pb.ServerInfoResponse
			r.Server = &s
			r.Info = &i
			result.Responses = append(result.Responses, &r)
		}
		b, _ := result.Marshal()
		w.Write(b)
	case "", "json":
		w.Header().Set("Content-Type", contentTypeJSON)
		jEnc := json.NewEncoder(w)
		jEnc.Encode(infos)
	}
}

func lbCheckHandler(w http.ResponseWriter, req *http.Request) {

	logger.Traceln("loadbalancer: ", req.URL.RequestURI())

	fmt.Fprintf(w, "Ok\n")
}

func stripCommentHeader(cfg []byte) []byte {

	// strip out the comment header block that begins with '#' characters
	// as soon as we see a line that starts with something _other_ than '#', we're done

	idx := 0
	for cfg[0] == '#' {
		idx = bytes.Index(cfg, []byte("\n"))
		if idx == -1 || idx+1 == len(cfg) {
			return nil
		}
		cfg = cfg[idx+1:]
	}

	return cfg
}

func main() {

	configFile := flag.String("c", "", "config file (json)")
	port := flag.Int("p", 0, "port to listen on")
	maxprocs := flag.Int("maxprocs", 0, "GOMAXPROCS")
	debugLevel := flag.Int("d", 0, "enable debug logging")
	logtostdout := flag.Bool("stdout", false, "write logging output also to stdout")
	logdir := flag.String("logdir", "/var/log/carbonzipper/", "logging directory")
	interval := flag.Duration("i", 0, "interval to report internal statistics to graphite")
	pidFile := flag.String("pid", "", "pidfile (default: empty, don't create pidfile)")

	flag.Parse()

	expvar.NewString("BuildVersion").Set(BuildVersion)

	if *configFile == "" {
		log.Fatal("missing config file")
	}

	cfgjs, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatal("unable to load config file:", err)
	}

	cfgjs = stripCommentHeader(cfgjs)

	if cfgjs == nil {
		log.Fatal("error removing header comment from ", *configFile)
	}

	err = json.Unmarshal(cfgjs, &Config)
	if err != nil {
		log.Fatal("error parsing config file: ", err)
	}

	if len(Config.Backends) == 0 {
		log.Fatal("no Backends loaded -- exiting")
	}

	// command line overrides config file

	if *port != 0 {
		Config.Port = *port
	}

	if *maxprocs != 0 {
		Config.MaxProcs = *maxprocs
	}

	if *interval == 0 {
		*interval = time.Duration(Config.IntervalSec) * time.Second
	}

	if *logdir == "" {
		mlog.SetRawStream(os.Stdout)
	} else {
		mlog.SetOutput(*logdir, "carbonzipper", *logtostdout)
	}

	searchConfigured = len(Config.SearchPrefix) > 0 && len(Config.SearchBackend) > 0

	logger = mlog.Level(*debugLevel)
	logger.Logln("starting carbonzipper", BuildVersion)

	logger.Logln("setting GOMAXPROCS=", Config.MaxProcs)
	runtime.GOMAXPROCS(Config.MaxProcs)

	logger.Logln("setting stats interval to", *interval)

	if Config.ConcurrencyLimitPerServer != 0 {
		logger.Logln("Setting concurrencyLimit", Config.ConcurrencyLimitPerServer)
		limiterServers := Config.Backends
		if searchConfigured {
			limiterServers = append(limiterServers, Config.SearchBackend)
		}
		Limiter = newServerLimiter(limiterServers, Config.ConcurrencyLimitPerServer)
	}

	// +1 to track every over the number of buckets we track
	timeBuckets = make([]int64, Config.Buckets+1)

	httputil.PublishTrackedConnections("httptrack")
	expvar.Publish("requestBuckets", expvar.Func(renderTimeBuckets))

	// export config via expvars
	expvar.Publish("Config", expvar.Func(func() interface{} { return Config }))

	Metrics.CacheSize = expvar.Func(func() interface{} { return Config.pathCache.ec.Size() })
	expvar.Publish("cacheSize", Metrics.CacheSize)

	Metrics.CacheItems = expvar.Func(func() interface{} { return Config.pathCache.ec.Items() })
	expvar.Publish("cacheItems", Metrics.CacheItems)

	http.HandleFunc("/metrics/find/", httputil.TrackConnections(httputil.TimeHandler(findHandler, bucketRequestTimes)))
	http.HandleFunc("/render/", httputil.TrackConnections(httputil.TimeHandler(renderHandler, bucketRequestTimes)))
	http.HandleFunc("/info/", httputil.TrackConnections(httputil.TimeHandler(infoHandler, bucketRequestTimes)))
	http.HandleFunc("/lb_check", lbCheckHandler)

	// nothing in the config? check the environment
	if Config.GraphiteHost == "" {
		if host := os.Getenv("GRAPHITEHOST") + ":" + os.Getenv("GRAPHITEPORT"); host != ":" {
			Config.GraphiteHost = host
		}
	}

	// only register g2g if we have a graphite host
	if Config.GraphiteHost != "" {

		logger.Logln("Using graphite host", Config.GraphiteHost)

		// register our metrics with graphite
		graphite := g2g.NewGraphite(Config.GraphiteHost, *interval, 10*time.Second)

		hostname, _ := os.Hostname()
		hostname = strings.Replace(hostname, ".", "_", -1)

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.find_requests", hostname), Metrics.FindRequests)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.find_errors", hostname), Metrics.FindErrors)

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.render_requests", hostname), Metrics.RenderRequests)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.render_errors", hostname), Metrics.RenderErrors)

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.info_requests", hostname), Metrics.InfoRequests)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.info_errors", hostname), Metrics.InfoErrors)

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.timeouts", hostname), Metrics.Timeouts)

		for i := 0; i <= Config.Buckets; i++ {
			graphite.Register(fmt.Sprintf("carbon.zipper.%s.requests_in_%dms_to_%dms", hostname, i*100, (i+1)*100), bucketEntry(i))
		}

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.cache_size", hostname), Metrics.CacheSize)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.cache_items", hostname), Metrics.CacheItems)

		go mstats.Start(*interval)

		graphite.Register(fmt.Sprintf("carbon.zipper.%s.alloc", hostname), &mstats.Alloc)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.total_alloc", hostname), &mstats.TotalAlloc)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.num_gc", hostname), &mstats.NumGC)
		graphite.Register(fmt.Sprintf("carbon.zipper.%s.pause_ns", hostname), &mstats.PauseNS)
	}

	// configure the storage client
	storageClient.Transport = &http.Transport{
		MaxIdleConnsPerHost: Config.MaxIdleConnsPerHost,
	}

	go probeTlds()
	// force run now
	probeForce <- 1

	go Config.pathCache.ec.ApproximateCleaner(10 * time.Second)

	if *pidFile != "" {
		pidfile.SetPidfilePath(*pidFile)
		err = pidfile.Write()
		if err != nil {
			log.Fatalln("error during pidfile.Write():", err)
		}
	}

	portStr := fmt.Sprintf(":%d", Config.Port)
	logger.Logln("listening on", portStr)

	err = gracehttp.Serve(&http.Server{
		Addr:    portStr,
		Handler: nil,
	})

	if err != nil {
		log.Fatalln("error during gracehttp.Serve():", err)
	}
}

var timeBuckets []int64

type bucketEntry int

func (b bucketEntry) String() string {
	return strconv.Itoa(int(atomic.LoadInt64(&timeBuckets[b])))
}

func renderTimeBuckets() interface{} {
	return timeBuckets
}

func bucketRequestTimes(req *http.Request, t time.Duration) {

	ms := t.Nanoseconds() / int64(time.Millisecond)

	bucket := int(ms / 100)

	if bucket < Config.Buckets {
		atomic.AddInt64(&timeBuckets[bucket], 1)
	} else {
		// Too big? Increment overflow bucket and log
		atomic.AddInt64(&timeBuckets[Config.Buckets], 1)
		logger.Logf("Slow Request: %s: %s", t.String(), req.URL.String())
	}
}

type serverLimiter map[string]chan struct{}

func newServerLimiter(servers []string, l int) serverLimiter {
	sl := make(map[string]chan struct{})

	for _, s := range servers {
		sl[s] = make(chan struct{}, l)
	}

	return sl
}

func (sl serverLimiter) enter(s string) {
	if sl == nil {
		return
	}
	sl[s] <- struct{}{}
}

func (sl serverLimiter) leave(s string) {
	if sl == nil {
		return
	}
	<-sl[s]
}

type pathCache struct {
	ec *expirecache.Cache
}

func (p *pathCache) set(k string, v []string) {
	// expire cache entries after 10 minutes
	const expireDelay = 60 * 10
	var size uint64
	for _, vv := range v {
		size += uint64(len(vv))
	}

	p.ec.Set(k, v, size, expireDelay)
}

func (p *pathCache) get(k string) ([]string, bool) {
	v, ok := p.ec.Get(k)
	if !ok {
		return nil, false
	}

	return v.([]string), true
}
