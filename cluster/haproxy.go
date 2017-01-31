package cluster

import (
	"fmt"
	"hash/crc64"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tanji/replication-manager/haproxy"
)

func (cluster *Cluster) initHaproxy() {
	haproxyconfigPath := cluster.conf.HttpRoot
	haproxytemplateFile := "haproxy_config.template"
	haproxyconfigFile := "haproxy_new.cfg"
	haproxyjsonFile := "vamp_router.json"
	haproxypidFile := "haproxy-private.pid"
	haproxysockFile := "haproxy.stats.sock"
	haproxyerrorPagesDir := "error_pages"
	//	haproxymaxWorkDirSize := 50 // this value is based on (max socket path size - md5 hash length - pre and postfixes)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.conf.HaproxyBinaryPath,
		SockFile: filepath.Join(cluster.conf.HttpRoot, "/", haproxysockFile),
	}
	haConfig := haproxy.Config{
		TemplateFile:  filepath.Join(haproxyconfigPath, haproxytemplateFile),
		ConfigFile:    filepath.Join(haproxyconfigPath, haproxyconfigFile),
		JsonFile:      filepath.Join(haproxyconfigPath, haproxyjsonFile),
		ErrorPagesDir: filepath.Join(haproxyconfigPath, haproxyerrorPagesDir, "/"),
		PidFile:       filepath.Join(cluster.conf.HttpRoot, "/", haproxypidFile),
		SockFile:      filepath.Join(cluster.conf.HttpRoot, "/", haproxysockFile),
		WorkingDir:    filepath.Join(cluster.conf.HttpRoot + "/"),
	}

	log.Printf("Haproxy loading haproxy config at %s", haproxyconfigPath)
	err := haConfig.GetConfigFromDisk()
	if err != nil {
		log.Printf("Haproxy did not find an haproxy config...initializing new config")
		haConfig.InitializeConfig()
	}
	few := haproxy.Frontend{Name: "my_write_frontend", Mode: "tcp", DefaultBackend: "service_write", BindPort: cluster.conf.HaproxyWritePort, BindIp: cluster.conf.HaproxyWriteBindIp}
	if err := haConfig.AddFrontend(&few); err != nil {
		log.Printf("Failed to add frontend write ")
	} else {
		if err := haConfig.AddFrontend(&few); err != nil {
			log.Printf("Should return nil on already existing frontend")
		}

	}
	if result, _ := haConfig.GetFrontend("my_write_frontend"); result.Name != "my_write_frontend" {
		log.Printf("Failed to add frontend write")
	}
	bew := haproxy.Backend{Name: "service_write", Mode: "tcp"}
	haConfig.AddBackend(&bew)

	if _, err := haConfig.GetServer("service_write", "leader"); err != nil {
		// log.Printf("No leader")
	} else {
		// log.Printf("Found exiting leader removing")
	}

	p, _ := strconv.Atoi(cluster.master.Port)
	s := haproxy.ServerDetail{Name: "leader", Host: cluster.master.Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
	if err = haConfig.AddServer("service_write", &s); err != nil {
		//	log.Printf("Failed to add server to service_write ")
	}

	fer := haproxy.Frontend{Name: "my_read_frontend", Mode: "tcp", DefaultBackend: "service_read", BindPort: cluster.conf.HaproxyReadPort, BindIp: cluster.conf.HaproxyReadBindIp}
	if err := haConfig.AddFrontend(&fer); err != nil {
		log.Printf("Failed to add frontend read")
	} else {
		if err := haConfig.AddFrontend(&fer); err != nil {
			log.Printf("Should return nil on already existing frontend")
		}
	}
	if result, _ := haConfig.GetFrontend("my_read_frontend"); result.Name != "my_read_frontend" {
		log.Printf("Failed to get frontend")
	}
	/* End add front end */

	ber := haproxy.Backend{Name: "service_read", Mode: "tcp"}
	if err := haConfig.AddBackend(&ber); err != nil {
		log.Printf("Failed to add backend Read")
	}

	//var checksum64 string
	crcHost := crc64.MakeTable(crc64.ECMA)
	for _, server := range cluster.servers {

		p, _ := strconv.Atoi(server.Port)
		checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(server.Host+":"+server.Port), crcHost))
		s := haproxy.ServerDetail{Name: checksum64, Host: server.Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
		if err := haConfig.AddServer("service_read", &s); err != nil {
			log.Printf("Failed to add server")
		}

	}

	err = haConfig.Render()
	if err != nil {
		log.Fatal("Could not render initial haproxy config, exiting...")
		os.Exit(1)
	}

	if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
		log.Printf("Haproxy pidfile exists at %s, proceeding...", haConfig.PidFile)
	} else {
		log.Println("Created new pidfile...")
	}

	err = haRuntime.Reload(&haConfig)
	if err != nil {
		log.Fatal("Error while reloading haproxy: " + err.Error())
		os.Exit(1)
	}
}
