package config

// AppConfig is a struct that holds the group configuration of the application
type AppConfig struct {
	ProvAppAgents         string                      `mapstructure:"prov-app-agents" toml:"prov-app-agents" json:"provAppAgents"`
	ProvAppAgentsFailover string                      `mapstructure:"prov-app-agents-failover" toml:"prov-app-agents-failover" json:"provAppAgentsFailover"`
	ProvAppType           string                      `mapstructure:"prov-app-service-type" toml:"prov-app-service-type" json:"provAppServiceType"`
	ProvAppDiskPool       string                      `mapstructure:"prov-app-disk-pool" toml:"prov-app-disk-pool" json:"provAppDiskPool"`
	ProvAppDiskType       string                      `mapstructure:"prov-app-disk-type" toml:"prov-app-disk-type" json:"provAppDiskType"`
	ProvAppDiskSize       string                      `measurement:"G,bytes,required" mapstructure:"prov-app-disk-size" toml:"prov-app-disk-size" json:"provAppDiskSize"`
	ProvAppCpuCores       string                      `mapstructure:"prov-app-cpu-cores" toml:"prov-app-cpu-cores" json:"provAppCpuCores"`
	ProvAppMemory         string                      `measurement:"M,bytes,required" mapstructure:"prov-app-memory" toml:"prov-app-memory" json:"provAppMemory"`
	ProvAppVolumeData     string                      `mapstructure:"prov-app-volume-data" toml:"prov-app-volume-data" json:"provAppVolumeData"`
	AppHosts              string                      `mapstructure:"app-hosts" toml:"app-Hosts" json:"appHosts"`
	ProvAppNetIface       string                      `mapstructure:"prov-app-net-iface" toml:"prov-app-net-iface" json:"provAppNetIface"`
	ProvAppNetmask        string                      `mapstructure:"prov-app-net-mask" toml:"prov-app-net-mask" json:"provAppNetMask"`
	ProvAppGateway        string                      `mapstructure:"prov-app-net-gateway" toml:"prov-app-net-gateway" json:"provAppNetGateway"`
	ProvAppRouteAddr      string                      `mapstructure:"prov-app-route-addr" toml:"prov-app-route-addr" json:"provAppRouteAddr"`
	ProvAppRoutePort      string                      `mapstructure:"prov-app-route-port" toml:"prov-app-route-port" json:"provAppRoutePort"`
	ProvAppRouteMask      string                      `mapstructure:"prov-app-route-mask" toml:"prov-app-route-mask" json:"provAppRouteMask"`
	ProvAppRoutePolicy    string                      `mapstructure:"prov-app-route-policy" toml:"prov-app-route-policy" json:"provAppRoutePolicy"`
	OnPremiseStartScript  string                      `mapstructure:"on-premise-start-script" toml:"on-premise-start-script" json:"onPremiseStartScript"`
	OnPremiseStopScript   string                      `mapstructure:"on-premise-stop-script" toml:"on-premise-stop-script" json:"onPremiseStopScript"`
	SlapOSAppPartitions   string                      `mapstructure:"slapos-app-partitions" toml:"slapos-app-partitions" json:"slaposAppPartitions"`
	Sections              map[string]AppSectionConfig `mapstructure:"-" toml:"-" json:"-"`
}

func (ac *AppConfig) GetSection(section string) (AppSectionConfig, bool) {
	appsection, ok := ac.Sections[section]
	return appsection, ok
}

// AppSectionConfig is a struct that holds the configuration for each section (container) of the application
type AppSectionConfig struct {
	AppDockerImg             string `mapstructure:"app-docker-img" toml:"app-docker-img" json:"provAppDockerImg"`
	AppDockerRunArgs         string `mapstructure:"app-docker-run-args" toml:"app-docker-run-args" json:"provAppDockerRunArgs"`
	AppDockerDiskArgs        string `mapstructure:"app-docker-disk-args" toml:"app-docker-disk-args" json:"provAppDockerDiskArgs"`
	AppDockerVolumeArgs      string `mapstructure:"app-docker-volume-args" toml:"app-docker-volume-args" json:"provAppDockerVolumeArgs"`
	AppRunCommand            string `mapstructure:"app-run-command" toml:"app-run-command" json:"appRunCommand"`
	AppConfigGitCloneUrl     string `mapstructure:"app-config-git-clone-url" toml:"app-config-git-clone-url" json:"appConfigGitCloneUrl"`
	AppConfigGitUser         string `mapstructure:"app-config-git-user" toml:"app-config-git-user" json:"appConfigGitUser"`
	AppConfigGitPassword     string `mapstructure:"app-config-git-password" toml:"app-config-git-password" json:"appConfigGitPassword"`
	AppConfigGitBranch       string `mapstructure:"app-config-git-branch" toml:"app-config-git-branch" json:"appConfigGitBranch"`
	AppConfigSecretVariables string `mapstructure:"app-config-secret-variables" toml:"app-config-secret-variables" json:"appConfigSecretVariables"`
	AppConfigEnvVariables    string `mapstructure:"app-config-env-variables" toml:"app-config-env-variables" json:"appConfigEnvVariable"`
	AppConfigVolumes         string `mapstructure:"app-config-volumes" toml:"app-config-volumes" json:"appConfigVolumes"`
	AppDataGitCloneUrl       string `mapstructure:"app-data-git-clone-url" toml:"app-data-git-clone-url" json:"appDataGitCloneUrl"`
	AppDataGitUser           string `mapstructure:"app-data-git-user" toml:"app-data-git-user" json:"appDataGitUser"`
	AppDataGitPassword       string `mapstructure:"app-data-git-password" toml:"app-data-git-password" json:"appDataGitPassword"`
	AppDataGitBranch         string `mapstructure:"app-data-git-branch" toml:"app-data-git-branch" json:"appDataBranch"`
	AppDataVolumes           string `mapstructure:"app-data-volumes" toml:"app-data-volumes" json:"appDataVolumes"`
	AppLogVolumes            string `mapstructure:"app-log-volumes" toml:"app-log-volumes" json:"appLogVolumes"`
}
