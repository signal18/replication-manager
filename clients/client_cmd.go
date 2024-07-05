//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"syscall"

	"strings"
	"time"

	termbox "github.com/nsf/termbox-go"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/server"
	"github.com/signal18/replication-manager/utils/s18log"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	Version                      string
	FullVersion                  string
	Build                        string
	cliUser                      string
	cliPassword                  string
	cliHost                      string
	cliPort                      string
	cliCert                      string
	cliNoCheckCert               bool
	cliToken                     string
	cliClusters                  []string
	cliClusterIndex              int
	cliTlog                      s18log.TermLog
	cliTermlength                int
	cliServers                   []cluster.ServerMonitor
	cliMaster                    cluster.ServerMonitor
	cliSettings                  cluster.Cluster
	cliMonitor                   server.ReplicationManager
	cliUrl                       string
	cliTTestRun                  string
	cliTestShowTests             bool
	cliTeststopcluster           bool
	cliTeststartcluster          bool
	cliTestConvert               bool
	cliTestConvertFile           string
	cliTestResultDBCredential    string
	cliTestResultDBServer        string
	cliBootstrapTopology         string
	cliBootstrapCleanall         bool
	cliBootstrapWithProvisioning bool
	cliExit                      bool
	cliPrefMaster                string
	cliStatusErrors              bool
	cliServerID                  string
	cliServerSet                 string
	cliServerGet                 string
	cliServerAction              string
	cliConsoleServerIndex        int
	cliShowObjects               string
	cliConfirm                   string
	cfgGroup                     string
	memprofile                   string
	cpuprofile                   string
	// Provisoning to add flags for compile
	WithProvisioning      string = "OFF"
	WithArbitration       string = "OFF"
	WithArbitrationClient string = "OFF"
	WithProxysql          string = "OFF"
	WithHaproxy           string = "OFF"
	WithMaxscale          string = "OFF"
	WithMariadbshardproxy string = "OFF"
	WithMonitoring        string = "ON"
	WithMail              string = "ON"
	WithHttp              string = "ON"
	WithEnforce           string = "OFF"
	WithDeprecate         string = "ON"
	WithOpenSVC           string = "OFF"
	WithTarball           string
	WithEmbed             string = "ON"
	WithMySQLRouter       string
	WithSphinx            string = "OFF"
	WithBackup            string = "OFF"
	GoOS                  string = "linux"
	GoArch                string = "amd64"
	conf                  config.Config
)

type RequetParam struct {
	key   string
	value string
}

var rootClientCmd = &cobra.Command{
	Use:   "replication-manager-cli",
	Short: "Replication Manager tool for MariaDB and MySQL",
	// Copyright 2017-2021 SIGNAL18 CLOUD SAS
	Long: `replication-manager allows users to monitor interactively MariaDB 10.x and MySQL GTID replication health
and trigger slave to master promotion (aka switchover), or elect a new master in case of failure (aka failover).`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

func Execute() error {
	if err := rootClientCmd.Execute(); err != nil {
		return err
	}
	return nil
}

var versionClientCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the replication manager client version number",
	Long:  `All software has versions. This is ours`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Replication Manager " + Version + " for MariaDB 10.x and MySQL 5.7 Series")
		fmt.Println("Full Version: ", FullVersion)
		fmt.Println("Build Time: ", Build)
	},
}

var cliConn = http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	Timeout:   1800 * time.Second,
}

func cliGetpasswd() string {
	fmt.Print("Enter Password: ")
	bytePassword, _ := terminal.ReadPassword(int(syscall.Stdin))
	password := string(bytePassword)
	return strings.TrimSpace(password)
}

func cliInit(needcluster bool) {
	var err error

	cliToken, err = cliLogin()
	if err != nil {
		cliPassword = cliGetpasswd()

		cliToken, err = cliLogin()
		if err != nil {
			fmt.Printf("\n'%s'\n", err)
			os.Exit(14)
		}
	}
	cliClusters, err = cliGetClusters()
	if err != nil {
		log.WithError(err).Fatal()
		return
	}
	allCLusters, _ := cliGetAllClusters()
	if len(cliClusters) != 1 && needcluster && cfgGroup == "" {
		err = errors.New("No cluster specify")
		log.WithError(err).Fatal(fmt.Sprintf("No cluster specify use --cluster in values %s", allCLusters))
	}
	if cliClusterInServerList() == false {
		fmt.Println("Cluster not found")
		os.Exit(10)
	}
	if len(allCLusters) == 0 {
		fmt.Println("Cluster not found")
		os.Exit(10)
	}
	cliServers, err = cliGetServers()
	if err != nil || len(cliServers) == 0 {
		fmt.Println("Servers not found")
		log.WithError(err).Fatal()
		return
	}
}

func cliClusterInServerList() bool {
	if cfgGroup == "" {
		return true
	}
	var isValueInList func(value string, list []string) bool
	isValueInList = func(value string, list []string) bool {
		for i, v := range list {
			if v == value {
				cliClusterIndex = i
				return true
			}
		}
		return false
	}

	clinput := strings.Split(cfgGroup, ",")
	for _, ci := range clinput {
		if isValueInList(ci, cliClusters) == false {
			return false
		}
	}
	return true
}

func initServerApiFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliUser, "user", "admin", "User of replication-manager")
	cmd.Flags().StringVar(&cliPassword, "password", "repman", "Paswword of replication-manager")
	cmd.Flags().StringVar(&cliPort, "port", "10005", "TLS port of  replication-manager")
	cmd.Flags().StringVar(&cliHost, "host", "127.0.0.1", "Host of replication-manager")
	cmd.Flags().StringVar(&cliCert, "cert", "", "Public certificate")
	cmd.Flags().BoolVar(&cliNoCheckCert, "insecure", true, "Don't check certificate")
	viper.BindPFlags(cmd.Flags())
}

func initBootstrapFlags(cmd *cobra.Command) {
	initServerApiFlags(bootstrapCmd)
	bootstrapCmd.Flags().StringVar(&cliBootstrapTopology, "topology", "master-slave", "master-slave|master-slave-no-gtid|maxscale-binlog|multi-master|multi-tier-slave|multi-master-ring,multi-master-wsrep")
	bootstrapCmd.Flags().BoolVar(&cliBootstrapCleanall, "clean-all", false, "Reset all slaves and binary logs before bootstrapping")
	bootstrapCmd.Flags().BoolVar(&cliBootstrapWithProvisioning, "with-provisioning", false, "Provision the culster for replication-manager-tst or Provision the culster for replication-manager-pro")
	viper.BindPFlags(cmd.Flags())
}

func initFailoverFlags(cmd *cobra.Command) {
	initServerApiFlags(failoverCmd)
	viper.BindPFlags(cmd.Flags())
}

func initRegTestFlags(cmd *cobra.Command) {
	initServerApiFlags(regTestCmd)
	regTestCmd.Flags().StringVar(&cliTTestRun, "run-tests", "", "tests list to be run ")
	regTestCmd.Flags().StringVar(&cliTestResultDBServer, "result-db-server", "", "MariaDB MySQL host to store result")
	regTestCmd.Flags().StringVar(&cliTestResultDBCredential, "result-db-credential", "", "MariaDB MySQL user:password to store result")
	regTestCmd.Flags().BoolVar(&cliTestShowTests, "show-tests", false, "display tests list")
	regTestCmd.Flags().BoolVar(&cliTeststartcluster, "test-provision-cluster", true, "start the cluster between tests")
	regTestCmd.Flags().BoolVar(&cliTeststopcluster, "test-unprovision-cluster", true, "stop the cluster between tests")
	regTestCmd.Flags().BoolVar(&cliTestConvert, "convert", false, "convert test result to html")
	regTestCmd.Flags().StringVar(&cliTestConvertFile, "file", "", "test result.json")
	viper.BindPFlags(cmd.Flags())
}

func initShowFlags(cmd *cobra.Command) {
	initServerApiFlags(showCmd)
	showCmd.Flags().StringVar(&cliShowObjects, "get", "settings,clusters,servers,master,slaves,crashes,alerts", "get the following objects")
	viper.BindPFlags(cmd.Flags())
}

func initStatusFlags(cmd *cobra.Command) {
	initServerApiFlags(statusCmd)
	statusCmd.Flags().BoolVar(&cliStatusErrors, "with-errors", false, "Add json errors reporting")
	viper.BindPFlags(cmd.Flags())
}

func initConfiguratorFlags(cmd *cobra.Command) {
	initServerApiFlags(configuratorCmd)
	RepMan.AddFlags(configuratorCmd.Flags(), &conf)
	v := viper.GetViper()
	v.SetConfigType("toml")
	viper.BindPFlags(configuratorCmd.Flags())
	cmd.AddCommand(configuratorCmd)
	viper.BindPFlags(cmd.Flags())
}

func initSwitchoverFlags(cmd *cobra.Command) {
	initServerApiFlags(switchoverCmd)
	switchoverCmd.Flags().StringVar(&cliPrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")
	viper.BindPFlags(cmd.Flags())
}

func initApiFlags(cmd *cobra.Command) {
	initServerApiFlags(apiCmd)
	apiCmd.Flags().StringVar(&cliUrl, "url", "https://127.0.0.1:10005/api/clusters", "Url to rest API")
	viper.BindPFlags(cmd.Flags())
}

func initServerFlags(cmd *cobra.Command) {
	initServerApiFlags(serverCmd)
	serverCmd.Flags().StringVar(&cliServerID, "id", "", "server id")
	serverCmd.Flags().StringVar(&cliServerset, "set", "", "maintenance=on|maintenance=off|maintenance=switch|ignored=switch|prefered=switch")
	serverCmd.Flags().StringVar(&cliServerGet, "get", "", "processlist|slow-query|digest-statements-pfs|errors|status-delta|innodb-status|variables|meta-data-locks|query-response-time")
	serverCmd.Flags().StringVar(&cliServerAction, "action", "", "stop|start")

	viper.BindPFlags(cmd.Flags())
}

func initClusterFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Cluster (default is none)")
	viper.BindPFlags(cmd.Flags())
}

func init() {

	rootClientCmd.AddCommand(clientConsoleCmd)
	initServerApiFlags(clientConsoleCmd)
	initClusterFlags(clientConsoleCmd)

	rootClientCmd.AddCommand(switchoverCmd)
	initSwitchoverFlags(switchoverCmd)
	initClusterFlags(switchoverCmd)

	rootClientCmd.AddCommand(failoverCmd)
	initFailoverFlags(failoverCmd)
	initClusterFlags(failoverCmd)

	rootClientCmd.AddCommand(topologyCmd)
	initClusterFlags(topologyCmd)
	initServerApiFlags(topologyCmd)

	rootClientCmd.AddCommand(apiCmd)
	initApiFlags(apiCmd)
	initClusterFlags(apiCmd)

	rootClientCmd.AddCommand(regTestCmd)
	initRegTestFlags(regTestCmd)
	initClusterFlags(regTestCmd)

	rootClientCmd.AddCommand(statusCmd)
	initStatusFlags(statusCmd)

	rootClientCmd.AddCommand(bootstrapCmd)
	initBootstrapFlags(bootstrapCmd)
	initClusterFlags(bootstrapCmd)

	rootClientCmd.AddCommand(serverCmd)
	initServerFlags(serverCmd)
	initClusterFlags(serverCmd)

	rootClientCmd.AddCommand(showCmd)
	initShowFlags(showCmd)
	initClusterFlags(showCmd)

	rootClientCmd.AddCommand(configuratorCmd)
	initConfiguratorFlags(showCmd)

	rootClientCmd.AddCommand(versionClientCmd)

}

func cliGetClusters() ([]string, error) {
	var cl []string
	var err error
	cl, err = cliGetAllClusters()
	if err != nil {
		return cl, err
	}

	return cl, nil
}

func cliNewTbChan() chan termbox.Event {
	termboxChan := make(chan termbox.Event)
	go func() {
		for {
			termboxChan <- termbox.PollEvent()
		}
	}()
	return termboxChan
}

func cliAddTlog(dlogs []string) {
	cliTlog.Shrink()
	for _, dl := range dlogs {
		cliTlog.Add(dl)
	}
}

func cliDisplayHelp() {
	cliLogPrint("HELP : Ctrl-D  Print debug information")
	cliLogPrint("HELP : Ctrl-F  Failover")
	cliLogPrint("HELP : Ctrl-S  Switchover")
	cliLogPrint("HELP : Ctrl-M  Maintenance")
	cliLogPrint("HELP : Ctrl-N  Next Cluster")
	cliLogPrint("HELP : Ctrl-P  Previous Cluster")
	cliLogPrint("HELP : Ctrl-Q  Quit")
	cliLogPrint("HELP : Ctrl-C  Quit")
	cliLogPrint("HELP : Ctrl-I  Switch failover automatic/manual")
	cliLogPrint("HELP : Ctrl-R  Switch slaves read-only/read-write")
	cliLogPrint("HELP : Ctrl-V  Switch verbosity")
	cliLogPrint("HELP : Ctrl-E  Erase failover control")

}

func cliPrintLog(msg []string) {
	for _, c := range msg {
		log.Printf(c)
	}
}

func cliPrintTb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func cliPrintfTb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	cliPrintTb(x, y, fg, bg, s)
}

func cliLogin() (string, error) {

	urlpost := "https://" + cliHost + ":" + cliPort + "/api/login"
	var jsonStr = []byte(`{"username":"` + cliUser + `", "password":"` + cliPassword + `"}`)
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", errors.New("Wrong credentential")
	}

	type Result struct {
		Token string `json:"token"`
	}
	var r Result
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in login", err)
		return "", err
	}
	return r.Token, nil
}

func cliGetAllClusters() ([]string, error) {
	var r server.ReplicationManager
	var res []string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/monitor"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Authorization", bearer)

	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR", err)
		return res, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
		return res, err
	}
	//	log.Printf("%s", body)
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in cluster list", err)

		return res, err
	}
	return r.ClusterList, nil
}

func cliGetSettings() (cluster.Cluster, error) {
	var r cluster.Cluster
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + ""
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR in settings", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR in settings", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in settings", err)
		return r, err
	}
	return r, nil
}

func cliGetMonitor() (server.ReplicationManager, error) {
	var r server.ReplicationManager
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/monitor"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR in monitor", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR in monitor", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}

	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in monitor", err)
		return r, err
	}
	return r, nil
}

func cliGetServers() ([]cluster.ServerMonitor, error) {
	var r []cluster.ServerMonitor
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/servers"
	//log.Println("INFO ", urlpost)
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)

	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in getting servers", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliGetMaster() (cluster.ServerMonitor, error) {
	var r cluster.ServerMonitor
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/master"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return r, err
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR in getting master", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliGetLogs() ([]string, error) {
	var r []string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/logs"
	var bearer = "Bearer " + cliToken
	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR on getting logs ", err)
		return r, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR on getting logs", err)
		return r, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return r, errors.New("Wrong credentential")
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR on getting logs", err)
		return r, err
	}
	resp.Body.Close()
	return r, nil
}

func cliClusterCmd(command string, params []RequetParam) error {
	//var r string
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/" + command
	var bearer = "Bearer " + cliToken

	data := url.Values{}
	data.Add("customer_name", "value")
	if params != nil {
		for _, param := range params {
			data.Add(param.key, param.value)
		}
	}
	b := bytes.NewBuffer([]byte(data.Encode()))

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cliConn.Do(req)
	if err != nil {
		log.Println("ERROR", err)
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR", err)
		return err
	}
	cliTlog.Add(string(body))
	/*err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}*/
	return nil
}
