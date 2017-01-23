package g2g

import (
	"expvar"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	networkSeparator = "://"
)

// Graphite represents a Graphite server. You Register expvars
// in this struct, which will be published to the server on a
// regular interval.
type Graphite struct {
	network       string
	endpoint      string
	interval      time.Duration
	timeout       time.Duration
	connection    net.Conn
	vars          map[string]expvar.Var
	registrations chan namedVar
	shutdown      chan chan bool
}

// A namedVar couples an expvar (interface) with an "external" name.
type namedVar struct {
	name string
	v    expvar.Var
}

// splitEndpoint splits the provided endpoint string into its network and address
// parts. It will default to 'tcp' network to ensure backward compatibility when
// the endpoint is not prefixed with a network:// part.
func splitEndpoint(endpoint string) (string, string) {
	network := "tcp"
	idx := strings.Index(endpoint, networkSeparator)
	if idx != -1 {
		network, endpoint = endpoint[:idx], endpoint[idx+len(networkSeparator):]
	}
	return network, endpoint
}

// NewGraphite returns a Graphite structure with no active/registered
// variables being published.  The connection setup is lazy, e.g. it is
// done at the first metric submission.
// Endpoint should be of the format "network://host:port", eg. "tcp://stats:2003".
// Interval is the (best-effort) minimum duration between (sequential)
// publishments of Registered expvars. Timeout is per-publish-action.
func NewGraphite(endpoint string, interval, timeout time.Duration) *Graphite {
	network, endpoint := splitEndpoint(endpoint)
	g := &Graphite{
		network:       network,
		endpoint:      endpoint,
		interval:      interval,
		timeout:       timeout,
		connection:    nil,
		vars:          map[string]expvar.Var{},
		registrations: make(chan namedVar),
		shutdown:      make(chan chan bool),
	}
	go g.loop()
	return g
}

// Register registers an expvar under the given name. (Roughly) every
// interval, the current value of the given expvar will be published to
// Graphite under the given name.
func (g *Graphite) Register(name string, v expvar.Var) {
	g.registrations <- namedVar{name, v}
}

// Shutdown signals the Graphite structure to stop publishing
// Registered expvars.
func (g *Graphite) Shutdown() {
	q := make(chan bool)
	g.shutdown <- q
	<-q
}

func (g *Graphite) loop() {
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case nv := <-g.registrations:
			g.vars[nv.name] = nv.v
		case <-ticker.C:
			g.postAll()
		case q := <-g.shutdown:
			if g.connection != nil {
				g.connection.Close()
				g.connection = nil
			}
			q <- true
			return
		}
	}
}

// roundFloat will attempt to parse the passed string as a float.
// If it succeeds, it will return the same float, rounded at n decimal places.
// If it fails, it will return the original string.
func roundFloat(s string, n int) string {
	if len(strings.Split(s, ".")) != 2 {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	format := fmt.Sprintf("%%.%df", n)
	return fmt.Sprintf(format, f)
}

// postAll publishes all Registered expvars to the Graphite server.
func (g *Graphite) postAll() {
	for name, v := range g.vars {
		val := roundFloat(v.String(), 2)
		if err := g.postOne(name, val); err != nil {
			log.Printf("g2g: %s: %s", name, err)
		}
	}
}

// postOne publishes the given name-value pair to the Graphite server.
// If the connection is broken, one reconnect attempt is made.
func (g *Graphite) postOne(name, value string) error {
	if g.connection == nil {
		if err := g.reconnect(); err != nil {
			return fmt.Errorf("failed; reconnect attempt: %s", err)
		}
	}
	deadline := time.Now().Add(g.timeout)
	if err := g.connection.SetWriteDeadline(deadline); err != nil {
		g.connection = nil
		return fmt.Errorf("SetWriteDeadline: %s", err)
	}
	b := []byte(fmt.Sprintf("%s %s %d\n", name, value, time.Now().Unix()))
	if n, err := g.connection.Write(b); err != nil {
		g.connection = nil
		return fmt.Errorf("Write: %s", err)
	} else if n != len(b) {
		g.connection = nil
		return fmt.Errorf("%s = %v: short write: %d/%d", name, value, n, len(b))
	}
	return nil
}

// reconnect attempts to (re-)establish a TCP connection to the Graphite server.
func (g *Graphite) reconnect() error {
	conn, err := net.Dial(g.network, g.endpoint)
	if err != nil {
		return err
	}
	g.connection = conn
	return nil
}
