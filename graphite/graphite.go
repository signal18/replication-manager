// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package graphite

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/signal18/replication-manager/graphite/carbon"
	"github.com/signal18/replication-manager/graphite/logging"
)

import _ "net/http/pprof"

// Graphite is a struct that defines the relevant properties of a graphite
// connection
type Graphite struct {
	Host     string
	Port     int
	Protocol string
	Timeout  time.Duration
	Prefix   string
	conn     net.Conn
	nop      bool
}

// defaultTimeout is the default number of seconds that we're willing to wait
// before forcing the connection establishment to fail
const defaultTimeout = 1

// IsNop is a getter for *graphite.Graphite.nop
func (graphite *Graphite) IsNop() bool {
	if graphite.nop {
		return true
	}
	return false
}

// Given a Graphite struct, Connect populates the Graphite.conn field with an
// appropriate TCP connection
func (graphite *Graphite) Connect() error {
	if !graphite.IsNop() {
		if graphite.conn != nil {
			graphite.conn.Close()
		}

		address := fmt.Sprintf("%s:%d", graphite.Host, graphite.Port)

		if graphite.Timeout == 0 {
			graphite.Timeout = defaultTimeout * time.Second
		}

		conn, err := net.DialTimeout(graphite.Protocol, address, graphite.Timeout)
		if err != nil {
			return err
		}

		graphite.conn = conn
	}

	return nil
}

// Given a Graphite struct, Disconnect closes the Graphite.conn field
func (graphite *Graphite) Disconnect() error {
	err := graphite.conn.Close()
	graphite.conn = nil
	return err
}

// Given a Metric struct, the SendMetric method sends the supplied metric to the
// Graphite connection that the method is called upon
func (graphite *Graphite) SendMetric(metric Metric) error {
	metrics := make([]Metric, 1)
	metrics[0] = metric

	return graphite.sendMetrics(metrics)
}

// Given a slice of Metrics, the SendMetrics method sends the metrics, as a
// batch, to the Graphite connection that the method is called upon
func (graphite *Graphite) SendMetrics(metrics []Metric) error {
	return graphite.sendMetrics(metrics)
}

// sendMetrics is an internal function that is used to write to the TCP
// connection in order to communicate metrics to the remote Graphite host
func (graphite *Graphite) sendMetrics(metrics []Metric) error {
	if graphite.IsNop() {
		for _, metric := range metrics {
			log.Printf("Graphite: %s\n", metric)
		}
		return nil
	}
	zeroed_metric := Metric{} // ignore unintialized metrics
	buf := bytes.NewBufferString("")
	for _, metric := range metrics {
		if metric == zeroed_metric {
			continue // ignore unintialized metrics
		}
		if metric.Timestamp == 0 {
			metric.Timestamp = time.Now().Unix()
		}
		metric_name := ""
		if graphite.Prefix != "" {
			metric_name = fmt.Sprintf("%s.%s", graphite.Prefix, metric.Name)
		} else {
			metric_name = metric.Name
		}
		if graphite.Protocol == "udp" {
			bufString := bytes.NewBufferString(fmt.Sprintf("%s %s %d\n", metric_name, metric.Value, metric.Timestamp))
			graphite.conn.Write(bufString.Bytes())
			continue
		}
		buf.WriteString(fmt.Sprintf("%s %s %d\n", metric_name, metric.Value, metric.Timestamp))
	}
	if graphite.Protocol == "tcp" {
		_, err := graphite.conn.Write(buf.Bytes())
		//fmt.Print("Sent msg:", buf.String(), "'")
		if err != nil {
			return err
		}
	}
	return nil
}

// The SimpleSend method can be used to just pass a metric name and value and
// have it be sent to the Graphite host with the current timestamp
func (graphite *Graphite) SimpleSend(stat string, value string) error {
	metrics := make([]Metric, 1)
	metrics[0] = NewMetric(stat, value, time.Now().Unix())
	err := graphite.sendMetrics(metrics)
	if err != nil {
		return err
	}
	return nil
}

// NewGraphite is a factory method that's used to create a new Graphite
func NewGraphite(host string, port int) (*Graphite, error) {
	return GraphiteFactory("tcp", host, port, "")
}

// NewGraphiteWithMetricPrefix is a factory method that's used to create a new Graphite with a metric prefix
func NewGraphiteWithMetricPrefix(host string, port int, prefix string) (*Graphite, error) {
	return GraphiteFactory("tcp", host, port, prefix)
}

// When a UDP connection to Graphite is required
func NewGraphiteUDP(host string, port int) (*Graphite, error) {
	return GraphiteFactory("udp", host, port, "")
}

// NewGraphiteNop is a factory method that returns a Graphite struct but will
// not actually try to send any packets to a remote host and, instead, will just
// log. This is useful if you want to use Graphite in a project but don't want
// to make Graphite a requirement for the project.
func NewGraphiteNop(host string, port int) *Graphite {
	graphiteNop, _ := GraphiteFactory("nop", host, port, "")
	return graphiteNop
}

func GraphiteFactory(protocol string, host string, port int, prefix string) (*Graphite, error) {
	var graphite *Graphite

	switch protocol {
	case "tcp":
		graphite = &Graphite{Host: host, Port: port, Protocol: "tcp", Prefix: prefix}
	case "udp":
		graphite = &Graphite{Host: host, Port: port, Protocol: "udp", Prefix: prefix}
	case "nop":
		graphite = &Graphite{Host: host, Port: port, nop: true}
	}

	err := graphite.Connect()
	if err != nil {
		return nil, err
	}

	return graphite, nil
}

const Version = "0.9.0"

func httpServe(addr string) (func(), error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return nil, err
	}

	go http.Serve(listener, nil)
	return func() { listener.Close() }, nil
}

func RunCarbon(ShareDir string, DataDir string, GraphiteCarbonPort int, GraphiteCarbonLinkPort int, GraphiteCarbonPicklePort int, GraphiteCarbonPprofPort int, GraphiteCarbonServerPort int) {
	var err error

	input, err := ioutil.ReadFile(ShareDir + "/carbon.conf.template")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	output := bytes.Replace(input, []byte("{{.schemas}}"), []byte(ShareDir+"/schemas.conf"), -1)
	fullpath, err := filepath.Abs(DataDir + "/graphite")

	output2 := bytes.Replace(output, []byte("{{.datadir}}"), []byte(fullpath), -1)
	output3 := bytes.Replace(output2, []byte("{{.graphitecarbonport}}"), []byte(strconv.Itoa(GraphiteCarbonPort)), -1)
	output4 := bytes.Replace(output3, []byte("{{.graphitecarbonlinkport}}"), []byte(strconv.Itoa(GraphiteCarbonLinkPort)), -1)
	output5 := bytes.Replace(output4, []byte("{{.graphitecarbonpickleport}}"), []byte(strconv.Itoa(GraphiteCarbonPicklePort)), -1)
	output6 := bytes.Replace(output5, []byte("{{.graphitecarbonpprofport}}"), []byte(strconv.Itoa(GraphiteCarbonPprofPort)), -1)
	output7 := bytes.Replace(output6, []byte("{{.graphitecarbonserverport}}"), []byte(strconv.Itoa(GraphiteCarbonServerPort)), -1)

	if err = ioutil.WriteFile(DataDir+"/carbon.conf", output7, 0666); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	app := carbon.New(DataDir + "/carbon.conf")

	if err = app.ParseConfig(); err != nil {
		log.Fatal(err)
	}

	app.Config.Common.Logfile = DataDir + "/carbon.log"
	//	log.Fatal(app.Config.Whisper.SchemasFilename)
	cfg := app.Config

	var runAsUser *user.User
	if cfg.Common.User != "" {
		runAsUser, err = user.Lookup(cfg.Common.User)
		if err != nil {
			log.Fatal(err)
		}
	}

	if err := logging.SetLevel(cfg.Common.LogLevel); err != nil {
		log.Fatal(err)
	}

	if err := logging.PrepareFile(cfg.Common.Logfile, runAsUser); err != nil {
		logrus.Fatal(err)
	}

	if err := logging.SetFile(cfg.Common.Logfile); err != nil {
		logrus.Fatal(err)
	}

	if cfg.Pprof.Enabled {
		_, err = httpServe(cfg.Pprof.Listen)
		if err != nil {
			logrus.Fatal(err)
		}
	}

	if err = app.Start(); err != nil {
		logrus.Fatal(err)
	} else {
		logrus.Info("started")
	}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGUSR2)
		for {
			<-c
			app.DumpStop()
		}
	}()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGHUP)
		for {
			<-c
			logrus.Info("HUP received. Reload config")
			if err := app.ReloadConfig(); err != nil {
				logrus.Errorf("Config reload failed: %s", err.Error())
			} else {
				logrus.Info("Config successfully reloaded")
			}
		}
	}()

	app.Loop()

	logrus.Info("stopped")
}
