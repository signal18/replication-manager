package app

import (
	"net/http"
	"os"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/pflag"
)

var WebDevOpsConfig config.AppConfig

type AppWebDevOps struct {
	App
	StatusCode int       `json:"status_code"`
	LastCheck  time.Time `json:"last_check"`
}

func NewAppWebDevOps(placement int, cluster ClusterInterface, appConfig *config.AppConfig, appHost string) *AppWebDevOps {
	conf := cluster.GetConf()
	apl := new(AppWebDevOps)
	apl.SetPlacement(placement, appConfig.ProvAppAgents, appConfig.SlapOSAppPartitions, appConfig.AppHosts, appConfig.AppWeights)
	apl.Type = config.ConstAppNginx
	apl.Port = appConfig.ProvAppRoutePort
	apl.Name = appHost
	apl.Host = appHost
	if conf.ProvNetCNI {
		apl.Host = apl.Host + "." + cluster.GetName() + ".svc." + conf.ProvOrchestratorCluster
	}
	apl.User = appConfig.AppConfigGitUser
	apl.Pass = conf.GetDecryptedPassword("app-web-config-git-password", appConfig.AppConfigGitPassword)

	return apl
}

func (app *AppWebDevOps) AddFlags(flags *pflag.FlagSet, conf *config.AppConfig) {
	flags.StringVar(&conf.ProvAppType, "prov-web-type", "", "Application type")
	flags.StringVar(&conf.ProvAppDiskPool, "prov-web-disk-pool", "", "Application disk pool")
	flags.StringVar(&conf.ProvAppDiskType, "prov-web-disk-type", "", "Application disk type")
	flags.StringVar(&conf.ProvAppDockerImg, "prov-web-docker-img", "", "Application docker image")
	flags.StringVar(&conf.ProvAppAgents, "prov-web-agents", "", "Application agents")
	flags.StringVar(&conf.ProvAppDiskSize, "prov-web-disk-size", "", "Application disk size")
	flags.StringVar(&conf.ProvAppCpuCores, "prov-web-cpu-cores", "", "Application CPU cores")
	flags.StringVar(&conf.ProvAppMemory, "prov-web-memory", "", "Application memory")
	flags.StringVar(&conf.ProvAppVolumeData, "prov-web-volume-data", "", "Application volume data")
	flags.StringVar(&conf.ProvAppDockerRunArgs, "prov-web-docker-run-args", "", "Application docker run args")
	flags.StringVar(&conf.ProvAppAgentsFailover, "prov-web-agents-failover", "", "Application agents failover")
	flags.StringVar(&conf.ProvAppNetIface, "prov-web-net-iface", "", "Application net iface")
	flags.StringVar(&conf.ProvAppNetmask, "prov-web-net-mask", "", "Application net mask")
	flags.StringVar(&conf.ProvAppGateway, "prov-web-net-gateway", "", "Application net gateway")
	flags.StringVar(&conf.ProvAppRouteAddr, "prov-web-route-addr", "", "Application route addr")
	flags.StringVar(&conf.ProvAppRoutePort, "prov-web-route-port", "", "Application route port")
	flags.StringVar(&conf.ProvAppRouteMask, "prov-web-route-mask", "", "Application route mask")
	flags.StringVar(&conf.ProvAppRoutePolicy, "prov-web-route-policy", "", "Application route policy")
	flags.StringVar(&conf.AppHosts, "app-web-hosts", "", "Application hosts")
	flags.StringVar(&conf.AppRunCommand, "app-web-run-command", "", "Application run command")
	flags.StringVar(&conf.AppConfigGitCloneUrl, "app-web-config-git-clone-url", "", "Application config git clone url")
	flags.StringVar(&conf.AppConfigGitUser, "app-web-config-git-user", "", "Application config git user")
	flags.StringVar(&conf.AppConfigGitPassword, "app-web-config-git-password", "", "Application config git password")
	flags.StringVar(&conf.AppConfigGitBranch, "app-web-config-git-branch", "", "Application config git branch")
	flags.StringVar(&conf.AppConfigSecretVariables, "app-web-config-secret-variables", "", "Application config secret variables")
	flags.StringVar(&conf.AppConfigEnvVariables, "app-web-config-env-variables", "", "Application config env variables")
	flags.StringVar(&conf.AppConfigVolumes, "app-web-config-volumes", "", "Application config volumes")
	flags.StringVar(&conf.AppDataGitCloneUrl, "app-web-data-git-clone-url", "", "Application data git clone url")
	flags.StringVar(&conf.AppDataGitUser, "app-web-data-git-user", "", "Application data git user")
	flags.StringVar(&conf.AppDataGitPassword, "app-web-data-git-password", "", "Application data git password")
	flags.StringVar(&conf.AppDataGitBranch, "app-web-data-git-branch", "", "Application data git branch")
	flags.StringVar(&conf.AppDataVolumes, "app-web-data-volumes", "", "Application data volumes")
	flags.StringVar(&conf.AppLogVolumes, "app-web-log-volumes", "", "Application log volumes")
}

func (app *AppWebDevOps) Init() {
	webappdir := app.Datadir + "/var"

	if _, err := os.Stat(webappdir); os.IsNotExist(err) {
		app.GetAppConfig()
		os.Symlink(app.Datadir+"/init/data", webappdir)
	}
}

func (app *AppWebDevOps) Refresh() error {
	resp, err := http.Get(app.GetURL())
	status := StateWebDown
	statusCode := 0
	if err == nil {
		status = StateWebRunning
		statusCode = resp.StatusCode
		resp.Body.Close()
	}

	app.State = status
	app.StatusCode = statusCode
	app.LastCheck = time.Now()

	return nil
}

func (app *AppWebDevOps) Failover() {
	app.BackendsStateChange()
}

func (app *AppWebDevOps) BackendsStateChange() {
	app.Refresh()
}

func (app *AppWebDevOps) CertificatesReload() error {
	return nil
}
