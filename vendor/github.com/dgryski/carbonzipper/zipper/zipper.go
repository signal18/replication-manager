package zipper

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"strconv"
	"time"

	pb3 "github.com/go-graphite/carbonzipper/carbonzipperpb3"
	"github.com/go-graphite/carbonzipper/limiter"
	"github.com/go-graphite/carbonzipper/pathcache"
	cu "github.com/go-graphite/carbonzipper/util/apictx"
	util "github.com/go-graphite/carbonzipper/util/zipperctx"

	"strings"

	"github.com/lomik/zapwriter"
	"github.com/satori/go.uuid"
	"go.uber.org/zap"
)

// Timeouts is a global structure that contains configuration for zipper Timeouts
type Timeouts struct {
	Global       time.Duration `yaml:"global"`
	AfterStarted time.Duration `yaml:"afterStarted"`
	Connect      time.Duration `yaml:"connect"`
}

// CarbonSearch is a global structure that contains carbonsearch related configuration bits
type CarbonSearch struct {
	Backend string `yaml:"backend"`
	Prefix  string `yaml:"prefix"`
}

// Config is a structure that contains zipper-related configuration bits
type Config struct {
	ConcurrencyLimitPerServer int
	MaxIdleConnsPerHost       int
	Backends                  []string

	CarbonSearch CarbonSearch

	PathCache         pathcache.PathCache
	SearchCache       pathcache.PathCache
	Timeouts          Timeouts
	KeepAliveInterval time.Duration `yaml:"keepAliveInterval"`
}

// Zipper provides interface to Zipper-related functions
type Zipper struct {
	storageClient *http.Client
	// Limiter limits our concurrency to a particular server
	limiter     limiter.ServerLimiter
	probeTicker *time.Ticker
	ProbeQuit   chan struct{}
	ProbeForce  chan int

	timeoutAfterAllStarted time.Duration
	timeout                time.Duration
	timeoutConnect         time.Duration
	timeoutKeepAlive       time.Duration
	keepAliveInterval      time.Duration

	searchBackend    string
	searchConfigured bool
	searchPrefix     string

	pathCache   pathcache.PathCache
	searchCache pathcache.PathCache

	backends                  []string
	concurrencyLimitPerServer int
	maxIdleConnsPerHost       int

	sendStats func(*Stats)
}

// Stats provides zipper-related statistics
type Stats struct {
	Timeouts          int64
	FindErrors        int64
	RenderErrors      int64
	InfoErrors        int64
	SearchRequests    int64
	SearchCacheHits   int64
	SearchCacheMisses int64

	MemoryUsage int64

	CacheMisses int64
	CacheHits   int64
}

type nameLeaf struct {
	name string
	leaf bool
}

// NewZipper allows to create new Zipper
func NewZipper(sender func(*Stats), config *Config) *Zipper {
	logger := zapwriter.Logger("new_zipper")
	z := &Zipper{
		probeTicker: time.NewTicker(10 * time.Minute),
		ProbeQuit:   make(chan struct{}),
		ProbeForce:  make(chan int),

		sendStats: sender,

		pathCache:   config.PathCache,
		searchCache: config.SearchCache,

		storageClient:             &http.Client{},
		backends:                  config.Backends,
		searchBackend:             config.CarbonSearch.Backend,
		searchPrefix:              config.CarbonSearch.Prefix,
		searchConfigured:          len(config.CarbonSearch.Prefix) > 0 && len(config.CarbonSearch.Backend) > 0,
		concurrencyLimitPerServer: config.ConcurrencyLimitPerServer,
		maxIdleConnsPerHost:       config.MaxIdleConnsPerHost,
		keepAliveInterval:         config.KeepAliveInterval,
		timeoutAfterAllStarted:    config.Timeouts.AfterStarted,
		timeout:                   config.Timeouts.Global,
		timeoutConnect:            config.Timeouts.Connect,
	}

	logger.Info("zipper config",
		zap.Any("config", config),
	)

	if z.concurrencyLimitPerServer != 0 {
		limiterServers := z.backends
		if z.searchConfigured {
			limiterServers = append(limiterServers, z.searchBackend)
		}
		z.limiter = limiter.NewServerLimiter(limiterServers, z.concurrencyLimitPerServer)
	}

	// configure the storage client
	z.storageClient.Transport = &http.Transport{
		MaxIdleConnsPerHost: z.maxIdleConnsPerHost,
		DialContext: (&net.Dialer{
			Timeout:   z.timeoutConnect,
			KeepAlive: z.keepAliveInterval,
			DualStack: true,
		}).DialContext,
	}

	go z.probeTlds()

	z.ProbeForce <- 1
	return z
}

// ServerResponse contains response from the zipper
type ServerResponse struct {
	server   string
	response []byte
}

var errNoResponses = fmt.Errorf("No responses fetched from upstream")
var errNoMetricsFetched = fmt.Errorf("No metrics in the response")

func mergeResponses(responses []ServerResponse, stats *Stats) ([]string, *pb3.MultiFetchResponse) {
	logger := zapwriter.Logger("zipper_render")

	servers := make([]string, 0, len(responses))
	metrics := make(map[string][]pb3.FetchResponse)

	for _, r := range responses {
		var d pb3.MultiFetchResponse
		err := d.Unmarshal(r.response)
		if err != nil {
			logger.Error("error decoding protobuf response",
				zap.String("server", r.server),
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			stats.RenderErrors++
			continue
		}
		stats.MemoryUsage += int64(d.Size())
		for _, m := range d.Metrics {
			metrics[m.GetName()] = append(metrics[m.GetName()], m)
		}
		servers = append(servers, r.server)
	}

	var multi pb3.MultiFetchResponse

	if len(metrics) == 0 {
		return servers, nil
	}

	for name, decoded := range metrics {
		logger.Debug("decoded response",
			zap.String("name", name),
			zap.Any("decoded", decoded),
		)

		if len(decoded) == 1 {
			logger.Debug("only one decoded response to merge",
				zap.String("name", name),
			)
			m := decoded[0]
			multi.Metrics = append(multi.Metrics, m)
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

		mergeValues(&metric, decoded, stats)
		multi.Metrics = append(multi.Metrics, metric)
	}

	stats.MemoryUsage += int64(multi.Size())

	return servers, &multi
}

func mergeValues(metric *pb3.FetchResponse, decoded []pb3.FetchResponse, stats *Stats) {
	logger := zapwriter.Logger("zipper_render")

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
					zap.Int("metric_values", len(metric.Values)),
					zap.Int("response_values", len(m.Values)),
				)
				// TODO(dgryski): we should remove
				// decoded[other] from the list of responses to
				// consider but this assumes that decoded[0] is
				// the 'highest resolution' response and thus
				// the one we want to keep, instead of the one
				// we want to discard

				stats.RenderErrors++
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

func infoUnpackPB(responses []ServerResponse, stats *Stats) map[string]pb3.InfoResponse {
	logger := zapwriter.Logger("zipper_info").With(zap.String("handler", "info"))

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
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			stats.InfoErrors++
			continue
		}
		decoded[r.server] = d
	}

	logger.Debug("info request",
		zap.Any("decoded_response", decoded),
	)

	return decoded
}

func findUnpackPB(responses []ServerResponse, stats *Stats) ([]pb3.GlobMatch, map[string][]string) {
	logger := zapwriter.Logger("zipper_find").With(zap.String("handler", "findUnpackPB"))

	// metric -> [server1, ... ]
	paths := make(map[string][]string)
	seen := make(map[nameLeaf]bool)

	var metrics []pb3.GlobMatch
	for _, r := range responses {
		var metric pb3.GlobResponse
		err := metric.Unmarshal(r.response)
		if err != nil {
			logger.Error("error decoding protobuf response",
				zap.String("server", r.server),
				zap.Error(err),
			)
			logger.Debug("response hexdump",
				zap.String("response", hex.Dump(r.response)),
			)
			stats.FindErrors += 1
			continue
		}

		for _, match := range metric.Matches {
			n := nameLeaf{match.Path, match.IsLeaf}
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

func (z *Zipper) doProbe() {
	stats := &Stats{}
	logger := zapwriter.Logger("probe")
	// Generate unique ID on every restart
	uuid := uuid.NewV4()
	ctx := util.SetUUID(context.Background(), uuid.String())
	query := "/metrics/find/?format=protobuf&query=%2A"

	responses := z.multiGet(ctx, logger, z.backends, query, stats)

	if len(responses) == 0 {
		logger.Info("TLD Probe returned empty set")
		return
	}

	_, paths := findUnpackPB(responses, stats)

	z.sendStats(stats)

	incompleteResponse := false
	if len(responses) != len(z.backends) {
		incompleteResponse = true
	}

	logger.Info("TLD Probe run results",
		zap.String("carbonzipper_uuid", uuid.String()),
		zap.Int("paths_count", len(paths)),
		zap.Int("responses_received", len(responses)),
		zap.Int("backends", len(z.backends)),
		zap.Bool("incomplete_response", incompleteResponse),
	)

	// update our cache of which servers have which metrics
	for k, v := range paths {
		z.pathCache.Set(k, v)
		logger.Debug("TLD Probe",
			zap.String("path", k),
			zap.Strings("servers", v),
			zap.String("carbonzipper_uuid", uuid.String()),
		)
	}
}

func (z *Zipper) probeTlds() {
	for {
		select {
		case <-z.probeTicker.C:
			z.doProbe()
		case <-z.ProbeForce:
			z.doProbe()
		case <-z.ProbeQuit:
			z.probeTicker.Stop()
			return
		}
	}
}

func (z *Zipper) singleGet(ctx context.Context, logger *zap.Logger, uri, server string, ch chan<- ServerResponse, started chan<- struct{}) {
	logger = logger.With(zap.String("handler", "singleGet"))

	u, err := url.Parse(server + uri)
	if err != nil {
		logger.Error("error parsing uri",
			zap.String("uri", server+uri),
			zap.Error(err),
		)
		ch <- ServerResponse{server, nil}
		return
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		logger.Error("failed to create new request",
			zap.Error(err),
		)
	}
	req = cu.MarshalCtx(ctx, util.MarshalCtx(ctx, req))

	logger = logger.With(zap.String("query", server+"/"+uri))
	z.limiter.Enter(server)
	started <- struct{}{}
	defer z.limiter.Leave(server)
	resp, err := z.storageClient.Do(req.WithContext(ctx))
	if err != nil {
		logger.Error("query error",
			zap.Error(err),
		)
		ch <- ServerResponse{server, nil}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// carbonsserver replies with Not Found if we request a
		// metric that it doesn't have -- makes sense
		ch <- ServerResponse{server, nil}
		return
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("bad response code",
			zap.Int("response_code", resp.StatusCode),
		)
		ch <- ServerResponse{server, nil}
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Error("error reading body",
			zap.Error(err),
		)
		ch <- ServerResponse{server, nil}
		return
	}

	ch <- ServerResponse{server, body}
}

func (z *Zipper) multiGet(ctx context.Context, logger *zap.Logger, servers []string, uri string, stats *Stats) []ServerResponse {
	logger = logger.With(zap.String("handler", "multiGet"))
	logger.Debug("querying servers",
		zap.Strings("servers", servers),
		zap.String("uri", uri),
	)

	// buffered channel so the goroutines don't block on send
	ch := make(chan ServerResponse, len(servers))
	startedch := make(chan struct{}, len(servers))

	for _, server := range servers {
		go z.singleGet(ctx, logger, uri, server, ch, startedch)
	}

	var response []ServerResponse

	timeout := time.After(z.timeout)

	var responses int
	var started int

GATHER:
	for {
		select {
		case <-startedch:
			started++
			if started == len(servers) {
				timeout = time.After(z.timeoutAfterAllStarted)
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
			stats.Timeouts++
			break GATHER
		}
	}

	return response
}

func (z *Zipper) fetchCarbonsearchResponse(ctx context.Context, logger *zap.Logger, url string, stats *Stats) []string {
	// Send query to SearchBackend. The result is []queries for StorageBackends
	searchResponse := z.multiGet(ctx, logger, []string{z.searchBackend}, url, stats)
	m, _ := findUnpackPB(searchResponse, stats)

	queries := make([]string, 0, len(m))
	for _, v := range m {
		queries = append(queries, v.Path)
	}
	return queries
}

func (z *Zipper) Render(ctx context.Context, logger *zap.Logger, target string, from, until int32) (*pb3.MultiFetchResponse, *Stats, error) {
	stats := &Stats{}

	rewrite, _ := url.Parse("http://127.0.0.1/render/")

	v := url.Values{
		"target": []string{target},
		"format": []string{"protobuf"},
		"from":   []string{strconv.Itoa(int(from))},
		"until":  []string{strconv.Itoa(int(until))},
	}
	rewrite.RawQuery = v.Encode()

	var serverList []string
	var ok bool
	var responses []ServerResponse
	if z.searchConfigured && strings.HasPrefix(target, z.searchPrefix) {
		stats.SearchRequests++

		var metrics []string
		if metrics, ok = z.searchCache.Get(target); !ok || metrics == nil || len(metrics) == 0 {
			stats.SearchCacheMisses++
			findURL := &url.URL{Path: "/metrics/find/"}
			findValues := url.Values{}
			findValues.Set("format", "protobuf")
			findValues.Set("query", target)
			findURL.RawQuery = findValues.Encode()

			metrics = z.fetchCarbonsearchResponse(ctx, logger, findURL.RequestURI(), stats)
			z.searchCache.Set(target, metrics)
		} else {
			stats.SearchCacheHits++
		}

		for _, target := range metrics {
			v.Set("target", target)
			rewrite.RawQuery = v.Encode()

			// lookup the server list for this metric, or use all the servers if it's unknown
			if serverList, ok = z.pathCache.Get(target); !ok || serverList == nil || len(serverList) == 0 {
				stats.CacheMisses++
				serverList = z.backends
			} else {
				stats.CacheHits++
			}

			newResponses := z.multiGet(ctx, logger, serverList, rewrite.RequestURI(), stats)
			responses = append(responses, newResponses...)
		}
	} else {
		rewrite.RawQuery = v.Encode()

		// lookup the server list for this metric, or use all the servers if it's unknown
		if serverList, ok = z.pathCache.Get(target); !ok || serverList == nil || len(serverList) == 0 {
			stats.CacheMisses++
			serverList = z.backends
		} else {
			stats.CacheHits++
		}

		responses = z.multiGet(ctx, logger, serverList, rewrite.RequestURI(), stats)
	}

	for i := range responses {
		stats.MemoryUsage += int64(len(responses[i].response))
	}

	if len(responses) == 0 {
		return nil, stats, errNoResponses
	}

	servers, metrics := mergeResponses(responses, stats)

	if metrics == nil {
		return nil, stats, errNoMetricsFetched
	}

	z.pathCache.Set(target, servers)

	return metrics, stats, nil
}

func (z *Zipper) Info(ctx context.Context, logger *zap.Logger, target string) (map[string]pb3.InfoResponse, *Stats, error) {
	stats := &Stats{}
	var serverList []string
	var ok bool

	// lookup the server list for this metric, or use all the servers if it's unknown
	if serverList, ok = z.pathCache.Get(target); !ok || serverList == nil || len(serverList) == 0 {
		stats.CacheMisses++
		serverList = z.backends
	} else {
		stats.CacheHits++
	}

	rewrite, _ := url.Parse("http://127.0.0.1/info/")

	v := url.Values{
		"target": []string{target},
		"format": []string{"protobuf"},
	}
	rewrite.RawQuery = v.Encode()

	responses := z.multiGet(ctx, logger, serverList, rewrite.RequestURI(), stats)

	if len(responses) == 0 {
		stats.InfoErrors++
		return nil, stats, errNoResponses
	}

	infos := infoUnpackPB(responses, stats)
	return infos, stats, nil
}

func (z *Zipper) Find(ctx context.Context, logger *zap.Logger, query string) ([]pb3.GlobMatch, *Stats, error) {
	stats := &Stats{}
	queries := []string{query}

	rewrite, _ := url.Parse("http://127.0.0.1/metrics/find/")

	v := url.Values{
		"query":  queries,
		"format": []string{"protobuf"},
	}
	rewrite.RawQuery = v.Encode()

	if z.searchConfigured && strings.HasPrefix(query, z.searchPrefix) {
		stats.SearchRequests++
		// 'completer' requests are translated into standard Find requests with
		// a trailing '*' by graphite-web
		if strings.HasSuffix(query, "*") {
			searchCompleterResponse := z.multiGet(ctx, logger, []string{z.searchBackend}, rewrite.RequestURI(), stats)
			matches, _ := findUnpackPB(searchCompleterResponse, stats)
			// this is a completer request, and so we should return the set of
			// virtual metrics returned by carbonsearch verbatim, rather than trying
			// to find them on the stores
			return matches, stats, nil
		}
		var ok bool
		if queries, ok = z.searchCache.Get(query); !ok || queries == nil || len(queries) == 0 {
			stats.SearchCacheMisses++
			queries = z.fetchCarbonsearchResponse(ctx, logger, rewrite.RequestURI(), stats)
			z.searchCache.Set(query, queries)
		} else {
			stats.SearchCacheHits++
		}
	}

	var metrics []pb3.GlobMatch
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
		if backends, ok = z.pathCache.Get(tld); !ok || backends == nil || len(backends) == 0 {
			stats.CacheMisses++
			backends = z.backends
		} else {
			stats.CacheHits++
		}

		responses := z.multiGet(ctx, logger, backends, rewrite.RequestURI(), stats)

		if len(responses) == 0 {
			return nil, stats, errNoResponses
		}

		m, paths := findUnpackPB(responses, stats)
		metrics = append(metrics, m...)

		// update our cache of which servers have which metrics
		allServers := make([]string, 0)
		for k, v := range paths {
			z.pathCache.Set(k, v)
			allServers = append(allServers, v...)
		}
		z.pathCache.Set(query, allServers)
	}

	return metrics, stats, nil
}
