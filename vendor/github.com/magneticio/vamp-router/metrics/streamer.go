package metrics

import (
	"github.com/magneticio/vamp-router/haproxy"
	gologger "github.com/op/go-logging"
	"strconv"
	"strings"
	"time"
)

type Streamer struct {
	wantedMetrics []string
	haRuntime     *haproxy.Runtime
	pollFrequency int
	Clients       map[chan Metric]bool
	Log           *gologger.Logger
}

// Adds a client to which messages can be multiplexed.
func (s *Streamer) AddClient(c chan Metric) {
	s.Clients[c] = true
}

// Just sets the metrics we want for now...
func NewStreamer(haRuntime *haproxy.Runtime, frequency int, log *gologger.Logger) *Streamer {
	return &Streamer{
		Log:           log,
		wantedMetrics: []string{"scur", "qcur", "qmax", "smax", "slim", "ereq", "econ", "lastsess", "qtime", "ctime", "rtime", "ttime", "req_rate", "req_rate_max", "req_tot", "rate", "rate_lim", "rate_max", "hrsp_1xx", "hrsp_2xx", "hrsp_3xx", "hrsp_4xx", "hrsp_5xx"},
		haRuntime:     haRuntime,
		pollFrequency: frequency,
		Clients:       make(map[chan Metric]bool),
	}
}

// simple wrapper for the actual start command.
func (s *Streamer) Start() {

	defer s.StartProtected()
	s.StartProtected()

}

/*
  Generates an outgoing stream of discrete Metric struct values.
  This stream can then be consumed by other streams like Kafka or SSE.
  It also protects against crashes by recovering panics and restarting
  the routine again.
*/
func (s *Streamer) StartProtected() {

	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("Cannot read from Haproxy socket, retrying in 5 seconds")
			time.Sleep(5000 * time.Millisecond)
			s.StartProtected()
		}
	}()

	statsChannel := make(chan map[string]map[string]string, 1000)

	go ParseMetrics(statsChannel, s.Clients, s.wantedMetrics)

	for {
		// start pumping the stats into the channel
		stats, err := s.haRuntime.GetStats("all")
		if err != nil {
			s.Log.Error(err.Error())
		}
		statsChannel <- stats
		time.Sleep(time.Duration(s.pollFrequency) * time.Millisecond)
	}
}

/*
	Parses a []Stats and injects it into each Metric channel in a map of channels
*/

func ParseMetrics(statsChannel chan map[string]map[string]string, clients map[chan Metric]bool, wantedMetrics []string) {

	wantedFrontendMetric := make(map[string]bool)
	wantedFrontendMetric["ereq"] = true
	wantedFrontendMetric["rate_lim"] = true
	wantedFrontendMetric["req_rate_max"] = true
	wantedFrontendMetric["req_rate"] = true

	for {
		select {
		case stats := <-statsChannel:
			localTime := time.Now().Format(time.RFC3339)

			// for each proxy in the stats dump, pick out the wanted metrics.
			for _, proxy := range stats {

				// loop over all wanted metrics for the current proxy
				for _, metric := range wantedMetrics {

					// discard all empty metrics
					if proxy[metric] != "" {

						value := proxy[metric]
						svname := proxy["svname"]
						tags := []string{}
						pxnames := strings.Split(proxy["pxname"], "::")

						// allow only some FRONTEND metrics and all non-FRONTEND metrics
						if (svname == "FRONTEND" && wantedFrontendMetric[metric]) || svname != "FRONTEND" {

							// Compile tags
							// we tag the metrics according to the following scheme
							switch {

							//- if pxname has no "." separator, and svname is [BACKEND|FRONTEND] it is the top route or "endpoint"
							case len(pxnames) == 1 && (svname == "BACKEND" || svname == "FRONTEND"):
								tags = append(tags, "routes:"+proxy["pxname"], "route")

								EmitMetric(localTime, tags, metric, value, clients)

							//-if pxname has no "."  separator, and svname is not [BACKEND|FRONTEND] it is an "in between"
							// server that routes to the actual service via a socket.
							case len(pxnames) == 1 && (svname != "BACKEND" || svname != "FRONTEND"):
							// sockName := strings.Split(svname, ".")
							// tags = append(tags, "routes:"+proxy["pxname"], "socket_servers:"+sockName[1])

							// we dont emit this metrics currently
							// EmitMetric(localTime, tags, metric, value, counter, c)

							//- if pxname has a separator, and svname is [BACKEND|FRONTEND] it is a service
							case len(pxnames) > 1 && (svname == "BACKEND" || svname == "FRONTEND"):
								tags = append(tags, "routes:"+pxnames[0], "services:"+pxnames[1], "service")
								EmitMetric(localTime, tags, metric, value, clients)

							//- if svname is not [BACKEND|FRONTEND] its a SERVER in a SERVICE and we prepend it with "server:"
							case len(pxnames) > 1 && (svname != "BACKEND" && svname != "FRONTEND"):
								tags = append(tags, "routes:"+pxnames[0], "services:"+pxnames[1], "servers:"+svname, "server")
								EmitMetric(localTime, tags, metric, value, clients)
							}
						}
					}
				}
			}
		}
	}
}

func EmitMetric(time string, tags []string, metric string, value string, clients map[chan Metric]bool) {
	tags = append(tags, "metrics:"+metric)
	_type := "router-metric"
	metricValue, _ := strconv.Atoi(value)

	//debug
	// fmt.Println("%v => metric %v m: %v\n", time, tags[0], metricValue)
	for s, _ := range clients {
		s <- Metric{tags, metricValue, time, _type}
	}
}
