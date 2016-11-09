package main

import (
	"flag"
	"github.com/magneticio/vamp-router/api"
	"github.com/magneticio/vamp-router/haproxy"
	"github.com/magneticio/vamp-router/helpers"
	"github.com/magneticio/vamp-router/logging"
	"github.com/magneticio/vamp-router/metrics"
	"github.com/magneticio/vamp-router/tools"
	"github.com/magneticio/vamp-router/zookeeper"
	gologger "github.com/op/go-logging"
	"os"
	"path/filepath"
	"strconv"
)

const (
	templateFile   = "templates/haproxy_config.template"
	configFile     = "haproxy_new.cfg"
	jsonFile       = "vamp_router.json"
	pidFile        = "haproxy-private.pid"
	sockFile       = "haproxy.stats.sock"
	errorPagesDir  = "error_pages"
	maxWorkDirSize = 50 // this value is based on (max socket path size - md5 hash length - pre and postfixes)
)

var (
	// Set all commandline arguments
	port          int
	logPath       string
	configPath    string
	binaryPath    string
	kafkaHost     string
	kafkaPort     int
	zooConString  string
	zooConKey     string
	headless      bool
	log           *gologger.Logger
	workDir       helpers.WorkDir
	customWorkDir string
)

func init() {
	flag.IntVar(&port, "port", 10001, "Port/IP to use for the REST interface. Overrides $PORT0 env variable")
	flag.StringVar(&logPath, "logPath", "/var/log/vamp-router/vamp-router.log", "Location of the log file")
	flag.StringVar(&configPath, "configPath", "", "Location of configuration files, defaults to configuration/")
	flag.StringVar(&binaryPath, "binary", helpers.HaproxyLocation(), "Path to the HAproxy binary")
	flag.StringVar(&kafkaHost, "kafkaHost", "", "The hostname or ip address of the Kafka host")
	flag.IntVar(&kafkaPort, "kafkaPort", 9092, "The port of the Kafka host")
	flag.StringVar(&zooConString, "zooConString", "", "A zookeeper ensemble connection string")
	flag.StringVar(&zooConKey, "zooConKey", "magneticio/vamplb", "Zookeeper root key")
	flag.StringVar(&customWorkDir, "customWorkDir", "", "Custom working directory for sockets and pid files, default to data/")
	flag.BoolVar(&headless, "headless", false, "Run without any logging output to the console")
}

func main() {

	flag.Parse()

	// resolve flags and environment variables
	tools.SetValueFromEnv(&port, "VAMP_RT_PORT")
	tools.SetValueFromEnv(&logPath, "VAMP_RT_LOG_PATH")
	tools.SetValueFromEnv(&configPath, "VAMP_RT_CONFIG_PATH")
	tools.SetValueFromEnv(&binaryPath, "VAMP_RT_BINARY_PATH")
	tools.SetValueFromEnv(&kafkaHost, "VAMP_RT_KAFKA_HOST")
	tools.SetValueFromEnv(&kafkaPort, "VAMP_RT_KAFKA_PORT")
	tools.SetValueFromEnv(&zooConString, "VAMP_RT_ZOO_STRING")
	tools.SetValueFromEnv(&zooConKey, "VAMP_RT_ZOO_KEY")
	tools.SetValueFromEnv(&customWorkDir, "VAMP_RT_CUSTOM_WORKDIR")
	tools.SetValueFromEnv(&headless, "VAMP_RT_HEADLESS")

	// setup logging
	log = logging.ConfigureLog(logPath, headless)
	log.Info(logging.PrintLogo(Version))

	// 	Setup working dir. Use custom path if provided, otherwise use install dir as root.

	if len(customWorkDir) == 0 {

		installDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			log.Fatal(err)
		} else {
			customWorkDir = filepath.Join(installDir, "/data")
		}
		if err := workDir.Create(customWorkDir, maxWorkDirSize); err != nil {
			log.Fatal(err)
		}
	} else {

		log.Notice("Using custom workdir: " + customWorkDir)
		if err := workDir.Create(customWorkDir, maxWorkDirSize); err != nil {
			log.Fatal(err)
		}
	}

	/*
		HAproxy runtime and configuration setup
	*/

	// TODO: refactor haRuntime struct to just include haConfig
	// setup Haproxy runtime
	haRuntime := haproxy.Runtime{
		Binary:   binaryPath,
		SockFile: filepath.Join(workDir.Dir(), "/", sockFile),
	}

	// setup configuration. Use custom path if provided, otherwise use install dir
	if len(configPath) == 0 {
		installDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			log.Fatal(err)
		} else {
			configPath = filepath.Join(installDir, "configuration", "/")
		}
	}

	haConfig := haproxy.Config{
		TemplateFile:  filepath.Join(configPath, templateFile),
		ConfigFile:    filepath.Join(configPath, configFile),
		JsonFile:      filepath.Join(configPath, jsonFile),
		ErrorPagesDir: filepath.Join(configPath, errorPagesDir, "/"),
		PidFile:       filepath.Join(workDir.Dir(), "/", pidFile),
		SockFile:      filepath.Join(workDir.Dir(), "/", sockFile),
		WorkingDir:    filepath.Join(workDir.Dir() + "/"),
	}

	log.Notice("Attempting to load config at %s", configPath)
	err := haConfig.GetConfigFromDisk()

	if err != nil {
		log.Notice("Did not find a config...initializing empty config")
		haConfig.InitializeConfig()
	}

	err = haConfig.Render()
	if err != nil {
		log.Fatal("Could not render initial config, exiting...")
		os.Exit(1)
	}

	if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
		log.Notice("Pidfile exists at %s, proceeding...", haConfig.PidFile)
	} else {
		log.Notice("Created new pidfile...")
	}

	err = haRuntime.Reload(&haConfig)
	if err != nil {
		log.Fatal("Error while reloading haproxy: " + err.Error())
		os.Exit(1)
	}

	/*
		Metric streaming setup
	*/

	log.Notice("Initializing metric streams...")

	Stream := metrics.NewStreamer(&haRuntime, 3000, log)
	// Initialize the stream from a runtime
	// stream.Init(&haRuntime, 3000, log)

	// Setup Kafka if required
	if len(kafkaHost) > 0 {

		kafkaChannel := make(chan metrics.Metric)
		Stream.AddClient(kafkaChannel)

		kafka := metrics.KafkaProducer{Log: log}
		kafka.In(kafkaChannel)
		kafka.Start(kafkaHost, kafkaPort)

	}

	sseChannel := make(chan metrics.Metric, 10000)
	Stream.AddClient(sseChannel)

	// Always setup SSE Stream
	sseBroker := &metrics.SSEBroker{
		make(map[chan metrics.Metric]bool),
		make(chan (chan metrics.Metric)),
		make(chan (chan metrics.Metric)),
		sseChannel,
		log,
	}

	go sseBroker.Start()
	go Stream.Start()

	/*
		Zookeeper setup
	*/

	if len(zooConString) > 0 {

		log.Notice("Initializing Zookeeper connection to " + zooConString + zooConKey)
		zkClient := zookeeper.ZkClient{}
		err := zkClient.Init(zooConString, &haConfig, log)

		if err != nil {
			log.Error("Error initializing Zookeeper...")
		}
		zkClient.Watch(zooConKey)
	}

	/*
		Rest API setup
	*/
	log.Notice("Initializing REST API...")
	if restApi, err := api.CreateApi(log, &haConfig, &haRuntime, sseBroker, Version); err != nil {
		panic("failed to create REST Api")
	} else {
		restApi.Run("0.0.0.0:" + strconv.Itoa(port))
	}

}
