package config

import (
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"hash"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
)

// AppConfig is a struct that holds the group configuration of the application
type AppConfig struct {
	ProvAppAgents         string                       `mapstructure:"prov-app-agents" toml:"prov-app-agents" json:"provAppAgents"`
	ProvAppAgentsFailover string                       `mapstructure:"prov-app-agents-failover" toml:"prov-app-agents-failover" json:"provAppAgentsFailover"`
	ProvAppType           string                       `mapstructure:"prov-app-service-type" toml:"prov-app-service-type" json:"provAppServiceType"`
	ProvAppDiskPool       string                       `mapstructure:"prov-app-disk-pool" toml:"prov-app-disk-pool" json:"provAppDiskPool"`
	ProvAppDiskType       string                       `mapstructure:"prov-app-disk-type" toml:"prov-app-disk-type" json:"provAppDiskType"`
	ProvAppDiskSize       string                       `measurement:"G,bytes,required" mapstructure:"prov-app-disk-size" toml:"prov-app-disk-size" json:"provAppDiskSize"`
	ProvAppCpuCores       string                       `mapstructure:"prov-app-cpu-cores" toml:"prov-app-cpu-cores" json:"provAppCpuCores"`
	ProvAppMemory         string                       `measurement:"M,bytes,required" mapstructure:"prov-app-memory" toml:"prov-app-memory" json:"provAppMemory"`
	ProvAppVolumeData     string                       `mapstructure:"prov-app-volume-data" toml:"prov-app-volume-data" json:"provAppVolumeData"`
	ProvAppNetIface       string                       `mapstructure:"prov-app-net-iface" toml:"prov-app-net-iface" json:"provAppNetIface"`
	ProvAppNetmask        string                       `mapstructure:"prov-app-net-mask" toml:"prov-app-net-mask" json:"provAppNetMask"`
	ProvAppGateway        string                       `mapstructure:"prov-app-net-gateway" toml:"prov-app-net-gateway" json:"provAppNetGateway"`
	ProvAppRouteAddr      string                       `mapstructure:"prov-app-route-addr" toml:"prov-app-route-addr" json:"provAppRouteAddr"`
	ProvAppRoutePort      string                       `mapstructure:"prov-app-route-port" toml:"prov-app-route-port" json:"provAppRoutePort"`
	ProvAppRouteMask      string                       `mapstructure:"prov-app-route-mask" toml:"prov-app-route-mask" json:"provAppRouteMask"`
	ProvAppRoutePolicy    string                       `mapstructure:"prov-app-route-policy" toml:"prov-app-route-policy" json:"provAppRoutePolicy"`
	OnPremiseStartScript  string                       `mapstructure:"on-premise-start-script" toml:"on-premise-start-script" json:"onPremiseStartScript"`
	OnPremiseStopScript   string                       `mapstructure:"on-premise-stop-script" toml:"on-premise-stop-script" json:"onPremiseStopScript"`
	SlapOSAppPartitions   string                       `mapstructure:"slapos-app-partitions" toml:"slapos-app-partitions" json:"slaposAppPartitions"`
	SecretVariables       string                       `mapstructure:"app-config-secret-variables" toml:"app-config-secret-variables" json:"appConfigSecretVariables"`
	EnvVariables          string                       `mapstructure:"app-config-env-variables" toml:"app-config-env-variables" json:"appConfigEnvVariable"`
	AppDataGitCloneUrl    string                       `mapstructure:"app-data-git-clone-url" toml:"app-data-git-clone-url" json:"appDataGitCloneUrl"`
	AppDataGitUser        string                       `mapstructure:"app-data-git-user" toml:"app-data-git-user" json:"appDataGitUser"`
	AppDataGitPassword    string                       `mapstructure:"app-data-git-password" toml:"app-data-git-password" json:"appDataGitPassword"`
	AppDataGitBranch      string                       `mapstructure:"app-data-git-branch" toml:"app-data-git-branch" json:"appDataBranch"`
	AppConfigGitCloneUrl  string                       `mapstructure:"app-config-git-clone-url" toml:"app-config-git-clone-url" json:"appConfigGitCloneUrl"`
	AppConfigGitUser      string                       `mapstructure:"app-config-git-user" toml:"app-config-git-user" json:"appConfigGitUser"`
	AppConfigGitPassword  string                       `mapstructure:"app-config-git-password" toml:"app-config-git-password" json:"appConfigGitPassword"`
	AppConfigGitBranch    string                       `mapstructure:"app-config-git-branch" toml:"app-config-git-branch" json:"appConfigGitBranch"`
	AppConfigVolumes      string                       `mapstructure:"app-config-volumes" toml:"app-config-volumes" json:"appConfigVolumes"`
	SecretMap             map[string]map[string]Secret `mapstructure:"-",toml:"-" json:"-"`
	ImmutableFlagMap      map[string]interface{}       `mapstructure:"-" toml:"-" json:"-"`
	DefaultFlagMap        map[string]interface{}       `mapstructure:"-" toml:"-" json:"-"`
}

func (ac *AppConfig) GetImmutableChecksum() (hash.Hash, error) {
	var buf bytes.Buffer
	new_h := md5.New()

	Container := make([]string, 0)

	for k, v := range ac.ImmutableFlagMap {
		if _, ok := ac.SecretMap[k]; !ok {
			Container = append(Container, fmt.Sprintf("%s=%v", k, v))
		}
	}

	misc.SortKeysAsc(Container)

	enc := gob.NewEncoder(&buf)
	err := enc.Encode(Container)
	if err != nil {
		return new_h, err
	}

	_, err = buf.WriteTo(new_h)
	return new_h, err
}

func (ac *AppConfig) GetSecretChecksum() (hash.Hash, error) {
	var buf bytes.Buffer
	new_h := md5.New()

	Container := make([]string, 0)

	for k, v := range ac.SecretMap {
		var newv []string
		for k2, v2 := range v {
			newv = append(newv, fmt.Sprintf("%s:%s", k2, v2.Value))
		}
		misc.SortKeysAsc(newv)
		Container = append(Container, fmt.Sprintf("%s=%v", k, strings.Join(newv, ",")))
	}

	misc.SortKeysAsc(Container)

	enc := gob.NewEncoder(&buf)
	err := enc.Encode(Container)
	if err != nil {
		return new_h, err
	}

	_, err = buf.WriteTo(new_h)
	return new_h, err
}

// AppSectionConfig is a struct that holds the configuration for each section (container) of the application
type AppSectionConfig struct {
	DockerImg        string `mapstructure:"docker-img" toml:"docker-img" json:"provAppDockerImg"`
	DockerRunArgs    string `mapstructure:"docker-run-args" toml:"docker-run-args" json:"provAppDockerRunArgs"`
	DockerDiskArgs   string `mapstructure:"docker-disk-args" toml:"docker-disk-args" json:"provAppDockerDiskArgs"`
	DockerVolumeArgs string `mapstructure:"docker-volume-args" toml:"docker-volume-args" json:"provAppDockerVolumeArgs"`
	RunCommand       string `mapstructure:"docker-run-command" toml:"docker-run-command" json:"appRunCommand"`
}

func (conf *Config) ToAppConfig() AppConfig {
	return AppConfig{
		ProvAppAgents:     conf.ProvAppAgents,
		ProvAppCpuCores:   conf.ProvAppCpuCores,
		ProvAppMemory:     conf.ProvAppMemory,
		ProvAppDiskType:   conf.ProvAppDiskType,
		ProvAppDiskSize:   conf.ProvAppDiskSize,
		ProvAppVolumeData: conf.ProvAppVolumeData,
	}
}
