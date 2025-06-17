package cluster

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
)

func (cluster *Cluster) NewAppConfig(apphost, port string) *config.AppConfig {
	return &config.AppConfig{
		AppHost:           apphost,
		AppPort:           port,
		ProvAppDiskType:   "volume",
		ProvAppMem:        cluster.GetAppMemory(nil),
		ProvAppCores:      cluster.GetAppCores(nil),
		ProvAppDisk:       cluster.GetAppDisk(nil),
		ProvAppAgents:     cluster.GetAppAgents(nil),
		ProvAppVolumeData: cluster.GetAppVolumeData(nil),
	}
}

func (cluster *Cluster) LoadAppConfigs() error {
	dirname := filepath.Join(cluster.WorkingDir, "apps")

	// Check if the directory exists
	_, err := os.Stat(dirname)
	if os.IsNotExist(err) {
		// Create the directory if it does not exist
		err = os.MkdirAll(cluster.WorkingDir+"/apps", os.ModePerm)
		if err != nil {
			return err
		}
	}

	// Set the new configuration
	if cluster.Conf.Apps == nil {
		cluster.Conf.Apps = make([]*config.AppConfig, 0)
	}

	// Walk through the directory and load all the configuration files
	return filepath.WalkDir(dirname, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".toml" {
			appname := strings.TrimSuffix(filepath.Base(path), ".toml")
			_ = cluster.LoadAppConfig(dirname, appname)
		}
		return nil
	})
}

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (cluster *Cluster) LoadAppConfig(dirname, appname string) error {

	// Create a new configuration struct
	var appcnf config.AppConfig

	filename := filepath.Join(dirname, appname+".toml")

	// Load the configuration file
	_, err := os.Stat(filename)
	if err != nil {
		return err
	}

	// Open TOML file
	appViper := viper.New()
	appViper.SetConfigFile(filename)
	err = appViper.ReadInConfig()
	if err != nil {
		// If there is an error reading the TOML file don't change the configuration
		return err
	}

	// Decode TOML file into the configuration struct
	err = appViper.Unmarshal(&appcnf)
	if err != nil {
		// If there is an error decoding the TOML file don't change the configuration
		return err
	}

	cluster.Conf.Apps = append(cluster.Conf.Apps, &appcnf)

	errormap := config.ParseConfigMeasurement(&appcnf, cluster.Conf.DefaultFlagMap, cluster.Conf.MeasurementAutoClampLimit)
	if len(errormap) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error parsing app config %s: %v", appname, errormap)
	}

	return errormap
}

// // LoadConfig loads the configuration from a file to the configuration struct.
// // If the file does not exist, it will return an error.
// // If the file exists but cannot be read, it will return the old configuration and the error.
// func (cluster *Cluster) LoadDeploymentsConfig(dirpath, appname string, appcnf *config.AppConfig) error {

// 	// Create a new configuration struct
// 	var result config.Deployment
// 	dirname := filepath.Join(dirpath, appname)
// 	if _, err := os.Stat(dirname); os.IsNotExist(err) {
// 		os.MkdirAll(dirname, os.ModePerm)
// 	}

// 	filename := filepath.Join(dirpath, appname, "deployments.toml")

// 	// Load the configuration file
// 	_, err := os.Stat(filename)
// 	if err != nil {
// 		return err
// 	}

// 	// Open TOML file
// 	appViper := viper.New()
// 	appViper.SetConfigFile(filename)
// 	err = appViper.ReadInConfig()
// 	if err != nil {
// 		// If there is an error reading the TOML file don't change the configuration
// 		return err
// 	}

// 	// Decode TOML file into the configuration struct
// 	err = appViper.Unmarshal(&result)
// 	if err != nil {
// 		// If there is an error decoding the TOML file don't change the configuration
// 		return err
// 	}

// 	// Set the new configuration
// 	appcnf.Deployments = result

// 	for _, dep := range appcnf.Deployments {
// 		if dep.Variables == nil {
// 			dep.Variables = make([]config.VariableMapping, 0)
// 		}
// 		if dep.Path == nil {
// 			dep.Path = make([]config.PathMapping, 0)
// 		}
// 		if dep.Routes == nil {
// 			dep.Routes = make([]config.Route, 0)
// 		}
// 		if dep.GitClones == nil {
// 			dep.GitClones = make([]config.GitClone, 0)
// 		}
// 	}

// 	return nil
// }

func (cluster *Cluster) SaveAppConfigs() (bool, error) {
	var has_changed bool
	for _, app := range cluster.Apps {
		changed, err := cluster.SaveApp(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error saving app %s: %s", app.Name, err)
			// return err
		}

		if changed {
			has_changed = true
		}
	}
	return has_changed, nil
}

func (cluster *Cluster) SaveApp(app *App) (bool, error) {
	var has_changed bool
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	// Save the main configuration file
	changed, err := cluster.SaveAppConfigFile(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
		return false, err
	}

	if changed {
		has_changed = true
	}

	// // Save the deployment configuration file
	// changed, err = cluster.SaveAppDeploymentsFile(app)
	// if err != nil {
	// 	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
	// 	return has_changed, err
	// }

	if changed {
		has_changed = true
	}

	return has_changed, nil
}

func (cluster *Cluster) SaveAppConfigFile(app *App) (bool, error) {
	filePath := cluster.WorkingDir + "/apps/" + app.Name + ".toml"

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(app.AppConfig)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
		return false, err
	}

	// Load TOML and sort keys
	t, err := toml.LoadBytes(readconf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
		return false, err
	}

	// Write sorted values to file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
		}
		return false, err
	}
	defer file.Close()

	// t.Delete("deployments")
	t.WriteTo(file)

	return true, nil
}

// func (cluster *Cluster) SaveAppDeploymentsFile(app *App) (bool, error) {
// 	filePath := app.Datadir + "/deployments.toml"

// 	// Write sorted values to file
// 	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
// 	if err != nil {
// 		if os.IsPermission(err) {
// 			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
// 		} else {
// 			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
// 		}
// 		return false, err
// 	}
// 	defer file.Close()

// 	// Marshal and write TOML configuration
// 	readconf, err := toml.Marshal(app.GetAppConfig().Deployments)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
// 		return false, err
// 	}

// 	// Load TOML and sort keys
// 	t, err := toml.LoadBytes(readconf)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
// 		return false, err
// 	}

// 	_, err = t.WriteTo(file)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing toml: %s", err)
// 		return false, err
// 	}

// 	return true, nil
// }

func (cluster *Cluster) AddSeededApp(srv, port, dockerImg string) error {
	for _, app := range cluster.Conf.Apps {
		if app.AppHost == srv && app.AppPort == port {
			return errors.New("App already exists. If you want to add new deployment, please use the app deployment menu")
		}
	}

	appcnf := cluster.GetAppConfig(srv, port) // Get or initiate app config
	appcnf.ProvAppDockerImg = dockerImg
	cluster.Conf.Apps = append(cluster.Conf.Apps, appcnf)

	cluster.Lock()
	cluster.newAppList()
	cluster.Unlock()
	return nil
}

func (cluster *Cluster) GetAppAgents(app *App) string {
	var appCnf *config.AppConfig

	if app != nil {
		// Get the app config
		appCnf = app.GetAppConfig()
		if appCnf != nil && appCnf.ProvAppAgents != "" {
			// If the app config has agents, return them
			return appCnf.ProvAppAgents
		}
	}

	// If the app config does not have agents, return the cluster agents
	agents := cluster.Conf.ProvAppAgents
	if agents == "" {
		// If the cluster does not have agents, return the default agents
		agents = cluster.Conf.ProvAgents
	}

	if agents != "" && appCnf != nil {
		appCnf.ProvAppAgents = agents
	}

	return agents
}

func (cluster *Cluster) GetAppDisk(app *App) string {
	var appCnf *config.AppConfig

	if app != nil {
		// Get the app config
		appCnf = app.GetAppConfig()
		if appCnf != nil && appCnf.ProvAppDisk != "" {
			// If the app config has disk, return it
			return appCnf.ProvAppDisk
		}
	}

	// If the app config does not have disk, return the cluster disk
	disk := cluster.Conf.ProvAppDisk
	if disk == "" {
		// If the cluster does not have disk, return the default disk
		disk = cluster.Conf.ProvDisk
	}

	if disk != "" && appCnf != nil {
		appCnf.ProvAppDisk = disk
	}

	return disk
}

func (cluster *Cluster) GetAppVolumeData(app *App) string {
	var appCnf *config.AppConfig

	if app != nil {
		// Get the app config
		appCnf = app.GetAppConfig()
		if appCnf != nil && appCnf.ProvAppVolumeData != "" {
			// If the app config has volume data, return it
			return appCnf.ProvAppVolumeData
		}
	}

	// If the app config does not have volume data, return the cluster volume data
	volumeData := cluster.Conf.ProvAppVolumeData
	if volumeData == "" {
		// If the cluster does not have volume data, return the default volume data
		volumeData = cluster.Conf.ProvVolumeData
	}

	if volumeData != "" && appCnf != nil {
		appCnf.ProvAppVolumeData = volumeData
	}

	return volumeData
}

func (cluster *Cluster) GetAppMemory(app *App) string {
	var appCnf *config.AppConfig

	if app != nil {
		// Get the app config
		appCnf = app.GetAppConfig()
		if appCnf != nil && appCnf.ProvAppMem != "" {
			// If the app config has memory, return it
			return appCnf.ProvAppMem
		}
	}

	// If the app config does not have memory, return the cluster memory
	mem := cluster.Conf.ProvAppMem
	if mem == "" {
		// If the cluster does not have memory, return the default memory
		mem = cluster.Conf.ProvMem
	}

	if mem != "" && appCnf != nil {
		appCnf.ProvAppMem = mem
	}

	return mem
}

// GetAppCores returns the cores for the app.
func (cluster *Cluster) GetAppCores(app *App) string {
	var appCnf *config.AppConfig

	if app != nil {
		// Get the app config
		appCnf = app.GetAppConfig()
		if appCnf != nil && appCnf.ProvAppCores != "" {
			// If the app config has cores, return it
			return appCnf.ProvAppCores
		}
	}

	// If the app config does not have cores, return the cluster cores
	cores := cluster.Conf.ProvAppCores
	if cores == "" {
		// If the cluster does not have cores, return the default cores
		cores = cluster.Conf.ProvCores
	}

	if cores != "" && appCnf != nil {
		appCnf.ProvAppCores = cores
	}

	return cores
}

func (cluster *Cluster) refreshApps(wg *sync.WaitGroup) {

	// if !cluster.Conf.AppOn {
	// 	return // If the app module is not enabled, do not refresh apps
	// }

	// Refresh the apps
	for _, app := range cluster.Apps {
		if app != nil {
			wg.Add(1)
			go func(app *App, wg *sync.WaitGroup) {
				defer wg.Done()
				defer cluster.LogPanicToFile("refreshApps")
				err := app.Refresh()
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "Error refreshing app %s: %s", app.Name, err)
				}
			}(app, wg)
		}
	}
}
