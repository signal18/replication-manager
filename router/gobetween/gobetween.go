/**
 * main.go - entry point
 * @author Yaroslav Pogrebnyak <yyyaroslav@gmail.com>
 */
package gobetween

import (
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"runtime"
	"time"

	"./api"
	"./config"
	"./info"
	"./logging"
	"./manager"
	"./utils/codec"
)

/**
 * Version should be set while build using ldflags (see Makefile)
 */
var version string

/**
 * Initialize package
 */
func init() {

	// Set GOMAXPROCS if not set
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	// Init random seed
	rand.Seed(time.Now().UnixNano())

	// Save info
	info.Version = version
	info.StartTime = time.Now()

}

/**
 * Entry point
 */
func Run(string path) {

	log.Printf("gobetween v%s", version)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	var cfg config.Config
	if err = codec.Decode(string(data), &cfg, "json"); err != nil {
		log.Fatal(err)
	}
	// Configure logging
	logging.Configure(cfg.Logging.Output, cfg.Logging.Level)

	// Start API
	go api.Start((*cfg).Api)

	// Start manager
	go manager.Initialize(*cfg)

	// block forever
	<-(chan string)(nil)

}
