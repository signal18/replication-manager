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

	pb2 "github.com/dgryski/carbonzipper/carbonzipperpb"
	pb3 "github.com/dgryski/carbonzipper/carbonzipperpb3"
	"github.com/dgryski/carbonzipper/mstats"
	"github.com/dgryski/go-expirecache"
	"github.com/dgryski/httputil"
	"github.com/facebookgo/grace/gracehttp"
	"github.com/facebookgo/pidfile"
	pickle "github.com/kisielk/og-rek"
	"github.com/peterbourgon/g2g"

	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
)

var DefaultLoggerConfig = zapwriter.Config{
		Logger:           "",
		File:             "stdout",
		Level:            "info",
		Encoding:         "console",
		EncodingTime:     "iso8601",
		EncodingDuration: "seconds",
}

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

	GraphiteHost         string
	InternalMetricPrefix string

	pathCache   pathCache
	searchCache pathCache

	MaxIdleConnsPerHost int

	ConcurrencyLimitPerServer int
	ExpireDelaySec            int32
	Logger                    []zapwriter.Config
}{
	MaxProcs:    1,
	IntervalSec: 60,
	Port:        8080,
	Buckets:     10,

	TimeoutMs:                10000,
	TimeoutMsAfterAllStarted: 2000,

	MaxIdleConnsPerHost: 100,

	ExpireDelaySec: 10 * 60, // 10 minutes

	pathCache:   pathCache{ec: expirecache.New(0)},
	searchCache: pathCache{ec: expirecache.New(0)},
	Logger: []zapwriter.Config{DefaultLoggerConfig},
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

	CacheSize         expvar.Func
	CacheItems        expvar.Func
	CacheMisses       *expvar.Int
	CacheHits         *expvar.Int
	SearchCacheSize   expvar.Func
	SearchCacheItems  expvar.Func
	SearchCacheMisses *expvar.Int
	SearchCacheHits   *expvar.Int
}{
	FindRequests: expvar.NewInt("find_requests"),
	FindErrors:   expvar.NewInt("find_errors"),

	SearchRequests: expvar.NewInt("search_requests"),

	RenderRequests: expvar.NewInt("render_requests"),
	RenderErrors:   expvar.NewInt("render_errors"),

	InfoRequests: expvar.NewInt("info_requests"),
	InfoErrors:   expvar.NewInt("info_errors"),

	Timeouts: expvar.NewInt("timeouts"),

	CacheHits:         expvar.NewInt("cache_hits"),
	CacheMisses:       expvar.NewInt("cache_misses"),
	SearchCacheHits:   expvar.NewInt("search_cache_hits"),
	SearchCacheMisses: expvar.NewInt("search_cache_misses"),
}

// BuildVersion is defined at build and reported at startup and as expvar
var BuildVersion = "(development version)"

// Limiter limits our concurrency to a particular server
var Limiter serverLimiter

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
	logger := zapwriter.Logger("probe")
	query := "/metrics/find/?format=protobuf3&query=%2A"

	responses := multiGet("probe", Config.Backends, query)

	if len(responses) == 0 {
		return
	}

	_, paths := findUnpackPB(nil, responses)

	// update our cache of which servers have which metrics
	for k, v := range paths {
		Config.pathCache.set(k, v)
		logger.Debug("TLD Probe",
			zap.String("path", k),
			zap.Strings("servers", v),
		)
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

func singleGet(logName, uri, server string, ch chan<- serverResponse, started chan<- struct{}) {
	logger := zapwriter.Logger(logName).With(zap.String("handler", "singleGet"))

	u, err := url.Parse(server + uri)
	if err != nil {
		logger.Error("error parsing uri",
			zap.String("uri", server+uri),
			zap.Error(err),
		)
		ch <- serverResponse{server, nil}
		return
	}
	req := http.Request{
		URL:    u,
		Header: make(http.Header),
	}

	logger = logger.With(zap.String("query", server+"/"+uri))
	Limiter.enter(server)
	started <- struct{}{}
	defer Limiter.leave(server)
	resp, err := storageClient.Do(&req)
	if err != nil {
		logger.Error("query error",
			zap.Error(err),
		)
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
		logger.Error("bad response code",
			zap.Int("response_code", resp.StatusCode),
		)
		ch <- serverResponse{server, nil}
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Error("error reading body",
			zap.Error(err),
		)
		ch <- serverResponse{server, nil}
		return
	}

	ch <- serverResponse{server, body}
}

func multiGet(logName string, servers []string, uri string) []serverResponse {
	logger := zapwriter.Logger(logName).With(zap.String("handler", "multiGet"))
	logger.Debug("querying servers",
		zap.Strings("servers", servers),
		zap.String("uri", uri),
	)

	// buffered channel so the goroutines don't block on send
	ch := make(chan serverResponse, len(servers))
	startedch := make(chan struct{}, len(servers))

	for _, server := range servers {
		go singleGet(logName, uri, server, ch, startedch)
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

			var timeoutedServs []string
			for i := range servers {
				found := false
				for j := range servs {
					if servers[i] == servs[j] {
						found = true
						break
					}
				}
				if !found {
					timeoutedServs = append(timeoutedServs, servers[i])
				}
			}

			logger.Warn("timeout waiting for more responses",
				zap.String("uri", uri),
				zap.Strings("timeouted_servers", timeoutedServs),
				zap.Strings("answers_from_servers", servs),
			)
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

func findUnpackPB(req *http.Request, responses []serverResponse) ([]*pb3.GlobMatch, map[string][]string) {
	logger := zapwriter.Logger("find").With(zap.String("handler", "findUnpackPB"))

	// metric -> [server1, ... ]
	paths := make(map[string][]string)
	seen := make(map[nameleaf]bool)

	var metrics []*pb3.GlobMatch
	for _, r := range responses {
		var metric pb3.GlobResponse
		err := metric.Unmarshal(r.response)
		if err != nil && req != nil {
			logger.Error("error decoding protobuf response",
				zap.String("server", r.server),
				zap.String("request", req.URL.RequestURI()),
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			Metrics.FindErrors.Add(1)
			continue
		}

		for _, match := range metric.Matches {
			n := nameleaf{match.Path, match.IsLeaf}
			_, ok := seen[n]
			if !ok {
				// we haven't seen this name yet
				// add the metric to the list of metrics to return
				metrics = append(metrics, match)
				seen[n] = true
			}
			// add the server to the list of servers that know about this metric
			p := paths[match.Path]
			p = append(p, r.server)
			paths[match.Path] = p
		}
	}

	return metrics, paths
}

const (
	contentTypeJSON     = "application/json"
	contentTypeProtobuf = "application/x-protobuf"
	contentTypePickle   = "application/pickle"
)

func fetchCarbonsearchResponse(req *http.Request, rewrite *url.URL) []string {
	// Send query to SearchBackend. The result is []queries for StorageBackends
	searchResponse := multiGet("find", []string{Config.SearchBackend}, rewrite.RequestURI())
	m, _ := findUnpackPB(req, searchResponse)
	queries := make([]string, 0, len(m))
	for _, v := range m {
		queries = append(queries, v.Path)
	}
	return queries
}

func findHandler(w http.ResponseWriter, req *http.Request) {
	t0 := time.Now()
	logger := zapwriter.Logger("find").With(zap.String("handler", "find"))
	logger.Debug("got find request",
		zap.String("request", req.URL.RequestURI()),
	)

	Metrics.FindRequests.Add(1)

	originalQuery := req.FormValue("query")
	queries := []string{originalQuery}

	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	format := req.FormValue("format")

	accessLogger := zapwriter.Logger("access").With(
		zap.String("handler", "render"),
		zap.String("format", format),
		zap.String("target", originalQuery),
	)

	v.Set("format", "protobuf3")
	rewrite.RawQuery = v.Encode()

	if searchConfigured && strings.HasPrefix(queries[0], Config.SearchPrefix) {
		Metrics.SearchRequests.Add(1)
		// 'completer' requests are translated into standard Find requests with
		// a trailing '*' by graphite-web
		if strings.HasSuffix(queries[0], "*") {
			searchCompleterResponse := multiGet("find", []string{Config.SearchBackend}, rewrite.RequestURI())
			matches, _ := findUnpackPB(nil, searchCompleterResponse)
			// this is a completer request, and so we should return the set of
			// virtual metrics returned by carbonsearch verbatim, rather than trying
			// to find them on the stores
			encodeFindResponse(format, originalQuery, w, matches)
			accessLogger.Info("request served",
				zap.Int("http_code", http.StatusOK),
				zap.Duration("runtime_seconds", time.Since(t0)),
			)
			return
		}
		var ok bool
		if queries, ok = Config.searchCache.get(queries[0]); !ok || queries == nil || len(queries) == 0 {
			Metrics.SearchCacheMisses.Add(1)
			queries = fetchCarbonsearchResponse(req, rewrite)
			Config.searchCache.set(queries[0], queries)
		} else {
			Metrics.SearchCacheHits.Add(1)
		}
	}

	var metrics []*pb3.GlobMatch
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
			Metrics.CacheMisses.Add(1)
			backends = Config.Backends
		} else {
			Metrics.CacheHits.Add(1)
		}

		responses := multiGet("find", backends, rewrite.RequestURI())

		if len(responses) == 0 {
			logger.Error("error quering backends",
				zap.String("request", rewrite.RequestURI()),
			)
			accessLogger.Error("request failed",
				zap.String("reason", "no responses to query"),
				zap.Int("http_code", http.StatusInternalServerError),
				zap.Duration("runtime_seconds", time.Since(t0)),
			)
			http.Error(w, "find: error querying backends", http.StatusInternalServerError)
			return
		}

		m, paths := findUnpackPB(req, responses)
		metrics = append(metrics, m...)

		// update our cache of which servers have which metrics
		allServers := make([]string, 0)
		for k, v := range paths {
			Config.pathCache.set(k, v)
			allServers = append(allServers, v...)
		}
		Config.pathCache.set(originalQuery, allServers)
	}

	encodeFindResponse(format, originalQuery, w, metrics)
	accessLogger.Info("request served",
		zap.Int("http_code", http.StatusOK),
		zap.Duration("runtime_seconds", time.Since(t0)),
	)
}

func encodeFindResponse(format, query string, w http.ResponseWriter, metrics []*pb3.GlobMatch) {
	switch format {
	case "protobuf3":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb3.GlobResponse
		result.Name = query
		result.Matches = metrics
		b, _ := result.Marshal()
		w.Write(b)
	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb2.GlobResponse
		var matches []*pb2.GlobMatch
		for i := range metrics {
			matches = append(matches, &pb2.GlobMatch{
				Path:   &metrics[i].Path,
				IsLeaf: &metrics[i].IsLeaf,
			})
		}
		result.Name = &query
		result.Matches = matches
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
				"metric_path": metric.Path,
				"isLeaf":      metric.IsLeaf,
			}
			result = append(result, mm)
		}

		pEnc := pickle.NewEncoder(w)
		pEnc.Encode(result)
	}
}

func renderHandler(w http.ResponseWriter, req *http.Request) {
	t0 := time.Now()
	logger := zapwriter.Logger("render").With(zap.String("handler", "render"))

	logger.Debug("got render request",
		zap.String("request", req.URL.RequestURI()),
	)

	Metrics.RenderRequests.Add(1)

	req.ParseForm()
	target := req.FormValue("target")
	format := req.FormValue("format")

	accessLogger := zapwriter.Logger("access").With(
		zap.String("handler", "render"),
		zap.String("format", format),
		zap.String("target", target),
	)

	if target == "" {
		http.Error(w, "empty target", http.StatusBadRequest)
		accessLogger.Error("request failed",
			zap.String("reason", "empty target"),
			zap.Int("http_code", http.StatusBadRequest),
			zap.Duration("runtime_seconds", time.Since(t0)),
		)
		return
	}

	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	v.Set("format", "protobuf3")

	var serverList []string
	var ok bool
	var responses []serverResponse
	if searchConfigured && strings.HasPrefix(target, Config.SearchPrefix) {
		Metrics.SearchRequests.Add(1)

		var metrics []string
		if metrics, ok = Config.searchCache.get(target); !ok || metrics == nil || len(metrics) == 0 {
			Metrics.SearchCacheMisses.Add(1)
			findURL := &url.URL{Path: "/metrics/find/"}
			findValues := url.Values{}
			findValues.Set("format", "protobuf3")
			findValues.Set("query", target)
			findURL.RawQuery = findValues.Encode()

			metrics = fetchCarbonsearchResponse(req, findURL)
			Config.searchCache.set(target, metrics)
		} else {
			Metrics.SearchCacheHits.Add(1)
		}

		for _, target := range metrics {
			v.Set("target", target)
			rewrite.RawQuery = v.Encode()

			// lookup the server list for this metric, or use all the servers if it's unknown
			if serverList, ok = Config.pathCache.get(target); !ok || serverList == nil || len(serverList) == 0 {
				Metrics.CacheMisses.Add(1)
				serverList = Config.Backends
			} else {
				Metrics.CacheHits.Add(1)
			}

			responses = append(responses, multiGet("render", serverList, rewrite.RequestURI())...)
		}
	} else {
		rewrite.RawQuery = v.Encode()

		// lookup the server list for this metric, or use all the servers if it's unknown
		if serverList, ok = Config.pathCache.get(target); !ok || serverList == nil || len(serverList) == 0 {
			Metrics.CacheMisses.Add(1)
			serverList = Config.Backends
		} else {
			Metrics.CacheHits.Add(1)
		}

		responses = multiGet("render", serverList, rewrite.RequestURI())
	}

	if len(responses) == 0 {
		accessLogger.Error("request failed",
			zap.String("reason", "no results from backends"),
			zap.String("request", req.URL.RequestURI()),
			zap.Int("http_code", http.StatusInternalServerError),
			zap.Strings("backends:", serverList),
		)
		http.Error(w, "render: error querying backends", http.StatusInternalServerError)
		Metrics.RenderErrors.Add(1)
		return
	}

	servers, metrics := mergeResponses(req, responses)
	if metrics == nil {
		Metrics.RenderErrors.Add(1)
		accessLogger.Error("request failed",
			zap.String("reason", "no decoded response to merge"),
			zap.String("request", req.URL.RequestURI()),
			zap.Int("http_code", http.StatusInternalServerError),
			zap.Duration("runtime_seconds", time.Since(t0)),
		)
		http.Error(w, "no decoded responses to merge", http.StatusInternalServerError)
		return
	}

	Config.pathCache.set(target, servers)

	switch format {
	case "protobuf3":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		b, err := metrics.Marshal()
		if err != nil {
			logger.Error("error marshaling data",
				zap.Error(err),
			)
		}
		w.Write(b)

	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var metricsPb2 pb2.MultiFetchResponse
		for i := range metrics.Metrics {
			metricsPb2.Metrics = append(metricsPb2.Metrics, &pb2.FetchResponse{
				Name:      &metrics.Metrics[i].Name,
				StartTime: &metrics.Metrics[i].StartTime,
				StopTime:  &metrics.Metrics[i].StopTime,
				StepTime:  &metrics.Metrics[i].StepTime,
				Values:    metrics.Metrics[i].Values,
				IsAbsent:  metrics.Metrics[i].IsAbsent,
			})
		}
		b, err := metricsPb2.Marshal()
		if err != nil {
			logger.Error("error marshaling data",
				zap.Error(err),
			)
		}
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
	accessLogger.Info("request served",
		zap.Int("http_code", http.StatusOK),
		zap.Duration("runtime_seconds", time.Since(t0)),
	)
}

func createRenderResponse(metrics *pb3.MultiFetchResponse, missing interface{}) []map[string]interface{} {

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

func mergeResponses(req *http.Request, responses []serverResponse) ([]string, *pb3.MultiFetchResponse) {
	logger := zapwriter.Logger("render")

	servers := make([]string, 0, len(responses))
	metrics := make(map[string][]pb3.FetchResponse)

	for _, r := range responses {
		var d pb3.MultiFetchResponse
		err := d.Unmarshal(r.response)
		if err != nil {
			logger.Error("error decoding protobuf response",
				zap.String("server", r.server),
				zap.String("request", req.URL.RequestURI()),
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			Metrics.RenderErrors.Add(1)
			continue
		}
		for _, m := range d.Metrics {
			metrics[m.GetName()] = append(metrics[m.GetName()], *m)
		}
		servers = append(servers, r.server)
	}

	var multi pb3.MultiFetchResponse

	if len(metrics) == 0 {
		return servers, nil
	}

	for name, decoded := range metrics {
		logger.Debug("decoded response",
			zap.String("request", req.URL.RequestURI()),
			zap.String("name", name),
			zap.Any("decoded", decoded),
		)

		if len(decoded) == 1 {
			logger.Debug("only one decoded response to merge",
				zap.String("name", name),
				zap.String("request", req.URL.RequestURI()),
			)
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

	return servers, &multi
}

func mergeValues(req *http.Request, metric *pb3.FetchResponse, decoded []pb3.FetchResponse) {
	logger := zapwriter.Logger("render")

	var responseLengthMismatch bool
	for i := range metric.Values {
		if !metric.IsAbsent[i] || responseLengthMismatch {
			continue
		}

		// found a missing value, find a replacement
		for other := 1; other < len(decoded); other++ {

			m := decoded[other]

			if len(m.Values) != len(metric.Values) {
				logger.Error("unable to merge ovalues",
					zap.String("request", req.URL.RequestURI()),
					zap.Int("metric_values", len(metric.Values)),
					zap.Int("response_values", len(m.Values)),
				)
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

func infoUnpackPB(req *http.Request, format string, responses []serverResponse) map[string]pb3.InfoResponse {
	logger := zapwriter.Logger("info").With(zap.String("handler", "info"))

	decoded := make(map[string]pb3.InfoResponse)
	for _, r := range responses {
		if r.response == nil {
			continue
		}
		var d pb3.InfoResponse
		err := d.Unmarshal(r.response)
		if err != nil {
			logger.Error("error decoding protobuf response",
				zap.String("server", r.server),
				zap.String("request", req.URL.RequestURI()),
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			Metrics.InfoErrors.Add(1)
			continue
		}
		decoded[r.server] = d
	}

	logger.Debug("info request",
		zap.String("request", req.URL.RequestURI()),
		zap.Any("decoded_response", decoded),
	)

	return decoded
}

func infoHandler(w http.ResponseWriter, req *http.Request) {
	t0 := time.Now()
	logger := zapwriter.Logger("info").With(zap.String("handler", "info"))

	logger.Debug("request",
		zap.String("request", req.URL.RequestURI()),
	)

	Metrics.InfoRequests.Add(1)

	req.ParseForm()
	target := req.FormValue("target")

	if target == "" {
		http.Error(w, "info: empty target", http.StatusBadRequest)
		return
	}

	accessLogger := zapwriter.Logger("access").With(
		zap.String("handler", "info"),
		zap.String("target", target),
	)

	var serverList []string
	var ok bool

	// lookup the server list for this metric, or use all the servers if it's unknown
	if serverList, ok = Config.pathCache.get(target); !ok || serverList == nil || len(serverList) == 0 {
		Metrics.CacheMisses.Add(1)
		serverList = Config.Backends
	} else {
		Metrics.CacheHits.Add(1)
	}

	format := req.FormValue("format")
	rewrite, _ := url.ParseRequestURI(req.URL.RequestURI())
	v := rewrite.Query()
	v.Set("format", "protobuf3")
	rewrite.RawQuery = v.Encode()

	responses := multiGet("info", serverList, rewrite.RequestURI())

	if len(responses) == 0 {
		logger.Error("error querying backends",
			zap.String("request", req.URL.RequestURI()),
			zap.Strings("backends:", serverList),
		)
		accessLogger.Info("request failed",
			zap.String("reason", "error querying backends"),
			zap.Duration("runtime_seconds", time.Since(t0)),
		)
		http.Error(w, "info: error querying backends", http.StatusInternalServerError)
		Metrics.InfoErrors.Add(1)
		return
	}

	infos := infoUnpackPB(req, format, responses)

	switch format {
	case "protobuf3":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb3.ZipperInfoResponse
		result.Responses = make([]*pb3.ServerInfoResponse, len(infos))
		for s, i := range infos {
			var r pb3.ServerInfoResponse
			r.Server = s
			r.Info = &i
			result.Responses = append(result.Responses, &r)
		}
		b, _ := result.Marshal()
		w.Write(b)
	case "protobuf":
		w.Header().Set("Content-Type", contentTypeProtobuf)
		var result pb2.ZipperInfoResponse
		result.Responses = make([]*pb2.ServerInfoResponse, len(infos))
		for s, i := range infos {

			var r pb2.ServerInfoResponse

			var retentions []*pb2.Retention
			for idx := range i.Retentions {
				retentions = append(retentions, &pb2.Retention{
					SecondsPerPoint: &i.Retentions[idx].SecondsPerPoint,
					NumberOfPoints:  &i.Retentions[idx].NumberOfPoints,
				})
			}

			r.Server = &s
			r.Info = &pb2.InfoResponse{
				Name:              &i.Name,
				AggregationMethod: &i.AggregationMethod,
				MaxRetention:      &i.MaxRetention,
				XFilesFactor:      &i.XFilesFactor,
				Retentions:        retentions,
			}
			result.Responses = append(result.Responses, &r)
		}
		b, _ := result.Marshal()
		w.Write(b)
	case "", "json":
		w.Header().Set("Content-Type", contentTypeJSON)
		jEnc := json.NewEncoder(w)
		jEnc.Encode(infos)
	}
	accessLogger.Info("request served",
		zap.Duration("runtime_seconds", time.Since(t0)),
	)
}

func lbCheckHandler(w http.ResponseWriter, req *http.Request) {
	t0 := time.Now()
	logger := zapwriter.Logger("loadbalancer").With(zap.String("handler", "loadbalancer"))
	accessLogger := zapwriter.Logger("access").With(zap.String("handler", "loadbalancer"))
	logger.Debug("loadbalacner",
		zap.String("request", req.URL.RequestURI()),
	)

	fmt.Fprintf(w, "Ok\n")
	accessLogger.Info("request served",
		zap.Duration("runtime_seconds", time.Since(t0)),
	)
}

func stripCommentHeader(cfg []byte) []byte {

	// strip out the comment header block that begins with '#' characters
	// as soon as we see a line that starts with something _other_ than '#', we're done

	var idx int
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
	err := zapwriter.ApplyConfig([]zapwriter.Config{DefaultLoggerConfig})
	if err != nil {
		log.Fatal("Failed to initialize logger with default configuration")

	}
	logger := zapwriter.Logger("main")

	configFile := flag.String("c", "", "config file (json)")
	port := flag.Int("p", 0, "port to listen on")
	maxprocs := flag.Int("maxprocs", 0, "GOMAXPROCS")
	interval := flag.Duration("i", 0, "interval to report internal statistics to graphite")
	pidFile := flag.String("pid", "", "pidfile (default: empty, don't create pidfile)")

	flag.Parse()

	expvar.NewString("BuildVersion").Set(BuildVersion)

	if *configFile == "" {
		logger.Fatal("missing config file option")
	}

	cfgjs, err := ioutil.ReadFile(*configFile)
	if err != nil {
		logger.Fatal("unable to load config file:",
			zap.Error(err),
		)
	}

	cfgjs = stripCommentHeader(cfgjs)

	if cfgjs == nil {
		logger.Fatal("error removing header comment from ",
			zap.String("config_file", *configFile),
		)
	}

	err = json.Unmarshal(cfgjs, &Config)
	if err != nil {
		logger.Fatal("error parsing config file: ",
			zap.Error(err),
		)
	}

	if len(Config.Backends) == 0 {
		logger.Fatal("no Backends loaded -- exiting")
	}

	err = zapwriter.ApplyConfig(Config.Logger)
	if err != nil {
		logger.Fatal("Failed to apply config",
			zap.Any("config", Config.Logger),
			zap.Error(err),
		)
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

	searchConfigured = len(Config.SearchPrefix) > 0 && len(Config.SearchBackend) > 0

	portStr := fmt.Sprintf(":%d", Config.Port)

	logger = zapwriter.Logger("main")
	logger.Info("starting carbonzipper",
		zap.String("build_version", BuildVersion),
		zap.Int("GOMAXPROCS", Config.MaxProcs),
		zap.Duration("stats interval", *interval),
		zap.Int("concurency_limit_per_server", Config.ConcurrencyLimitPerServer),
		zap.String("graphite_host", Config.GraphiteHost),
		zap.String("listen_port", portStr),
	)

	runtime.GOMAXPROCS(Config.MaxProcs)

	if Config.ConcurrencyLimitPerServer != 0 {
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

	Metrics.SearchCacheSize = expvar.Func(func() interface{} { return Config.searchCache.ec.Size() })
	expvar.Publish("searchCacheSize", Metrics.SearchCacheSize)

	Metrics.SearchCacheItems = expvar.Func(func() interface{} { return Config.searchCache.ec.Items() })
	expvar.Publish("searchCacheItems", Metrics.SearchCacheItems)

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

	if Config.InternalMetricPrefix == "" {
		Config.InternalMetricPrefix = "carbon.zipper"
	}

	// only register g2g if we have a graphite host
	if Config.GraphiteHost != "" {
		// register our metrics with graphite
		graphite := g2g.NewGraphite(Config.GraphiteHost, *interval, 10*time.Second)

		hostname, _ := os.Hostname()
		hostname = strings.Replace(hostname, ".", "_", -1)

		prefix := Config.InternalMetricPrefix

		graphite.Register(fmt.Sprintf("%s.%s.find_requests", prefix, hostname), Metrics.FindRequests)
		graphite.Register(fmt.Sprintf("%s.%s.find_errors", prefix, hostname), Metrics.FindErrors)

		graphite.Register(fmt.Sprintf("%s.%s.render_requests", prefix, hostname), Metrics.RenderRequests)
		graphite.Register(fmt.Sprintf("%s.%s.render_errors", prefix, hostname), Metrics.RenderErrors)

		graphite.Register(fmt.Sprintf("%s.%s.info_requests", prefix, hostname), Metrics.InfoRequests)
		graphite.Register(fmt.Sprintf("%s.%s.info_errors", prefix, hostname), Metrics.InfoErrors)

		graphite.Register(fmt.Sprintf("%s.%s.timeouts", prefix, hostname), Metrics.Timeouts)

		for i := 0; i <= Config.Buckets; i++ {
			graphite.Register(fmt.Sprintf("%s.%s.requests_in_%dms_to_%dms", prefix, hostname, i*100, (i+1)*100), bucketEntry(i))
		}

		graphite.Register(fmt.Sprintf("%s.%s.cache_size", prefix, hostname), Metrics.CacheSize)
		graphite.Register(fmt.Sprintf("%s.%s.cache_items", prefix, hostname), Metrics.CacheItems)
		graphite.Register(fmt.Sprintf("%s.%s.cache_hits", prefix, hostname), Metrics.CacheHits)
		graphite.Register(fmt.Sprintf("%s.%s.cache_misses", prefix, hostname), Metrics.CacheMisses)

		graphite.Register(fmt.Sprintf("%s.%s.search_cache_size", prefix, hostname), Metrics.SearchCacheSize)
		graphite.Register(fmt.Sprintf("%s.%s.search_cache_items", prefix, hostname), Metrics.SearchCacheItems)
		graphite.Register(fmt.Sprintf("%s.%s.search_cache_hits", prefix, hostname), Metrics.SearchCacheHits)
		graphite.Register(fmt.Sprintf("%s.%s.search_cache_misses", prefix, hostname), Metrics.SearchCacheMisses)

		go mstats.Start(*interval)

		graphite.Register(fmt.Sprintf("%s.%s.alloc", prefix, hostname), &mstats.Alloc)
		graphite.Register(fmt.Sprintf("%s.%s.total_alloc", prefix, hostname), &mstats.TotalAlloc)
		graphite.Register(fmt.Sprintf("%s.%s.num_gc", prefix, hostname), &mstats.NumGC)
		graphite.Register(fmt.Sprintf("%s.%s.pause_ns", prefix, hostname), &mstats.PauseNS)
	}

	// configure the storage client
	storageClient.Transport = &http.Transport{
		MaxIdleConnsPerHost: Config.MaxIdleConnsPerHost,
	}

	go probeTlds()
	// force run now
	probeForce <- 1

	go Config.pathCache.ec.ApproximateCleaner(10 * time.Second)
	go Config.searchCache.ec.ApproximateCleaner(10 * time.Second)

	if *pidFile != "" {
		pidfile.SetPidfilePath(*pidFile)
		err = pidfile.Write()
		if err != nil {
			log.Fatalln("error during pidfile.Write():", err)
		}
	}

	err = gracehttp.Serve(&http.Server{
		Addr:    portStr,
		Handler: nil,
	})

	if err != nil {
		log.Fatal("error during gracehttp.Serve()",
			zap.Error(err),
		)
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
	logger := zapwriter.Logger("slow")

	ms := t.Nanoseconds() / int64(time.Millisecond)

	bucket := int(ms / 100)

	if bucket < Config.Buckets {
		atomic.AddInt64(&timeBuckets[bucket], 1)
	} else {
		// Too big? Increment overflow bucket and log
		atomic.AddInt64(&timeBuckets[Config.Buckets], 1)
		logger.Warn("Slow Request",
			zap.Duration("time", t),
			zap.String("url", req.URL.String()),
		)
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
	// expire cache entries after Config.ExpireCache minutes
	var size uint64
	for _, vv := range v {
		size += uint64(len(vv))
	}

	p.ec.Set(k, v, size, Config.ExpireDelaySec)
}

func (p *pathCache) get(k string) ([]string, bool) {
	v, ok := p.ec.Get(k)
	if !ok {
		return nil, false
	}

	return v.([]string), true
}
