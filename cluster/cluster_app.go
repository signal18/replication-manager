package cluster

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
)

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
		cluster.Conf.Apps = make(map[string]*config.AppConfig)
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

	cluster.LoadDeploymentsConfig(dirname, appname, &appcnf)

	cluster.Conf.Apps[appname] = &appcnf
	// Add the app to the cluster if it does not exist
	applist := strings.Split(cluster.Conf.AppHosts, ",")
	if !strings.Contains(cluster.Conf.AppHosts, appname) {
		applist = append(applist, appname)
		cluster.Conf.AppHosts = strings.Join(applist, ",")
	}

	return nil
}

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (cluster *Cluster) LoadDeploymentsConfig(dirpath, appname string, appcnf *config.AppConfig) error {

	// Create a new configuration struct
	var result map[string]*config.Deployment
	dirname := filepath.Join(dirpath, appname)
	if _, err := os.Stat(dirname); os.IsNotExist(err) {
		os.MkdirAll(dirname, os.ModePerm)
	}

	filename := filepath.Join(dirpath, appname, "deployments.toml")

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
	err = appViper.Unmarshal(&result)
	if err != nil {
		// If there is an error decoding the TOML file don't change the configuration
		return err
	}

	// Set the new configuration
	appcnf.Deployments = result

	return nil
}

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

	// Save the deployment configuration file
	changed, err = cluster.SaveAppDeploymentsFile(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
		return has_changed, err
	}

	if changed {
		has_changed = true
	}

	return has_changed, nil
}

func (cluster *Cluster) SaveAppConfigFile(app *App) (bool, error) {
	filePath := cluster.WorkingDir + "/apps/" + app.Name + ".toml"

	appcnf := app.GetAppConfig()

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(appcnf)
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

	t.Delete("deployments")
	t.WriteTo(file)

	return true, nil
}

func (cluster *Cluster) SaveAppDeploymentsFile(app *App) (bool, error) {

	filePath := app.Datadir + "/deployments.toml"

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

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(app.GetAppConfig().Deployments)
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

	_, err = t.WriteTo(file)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing toml: %s", err)
		return false, err
	}

	return true, nil
}

func (cluster *Cluster) AddSeededApp(srv string) error {
	if strings.Contains(cluster.Conf.AppHosts, srv) {
		return errors.New("App already exists")
	}

	hosts := strings.Split(cluster.Conf.AppHosts, ",")

	//Remove empty slices
	n := 0
	for i := range hosts {
		if hosts[i] != "" {
			hosts[n] = hosts[i]
			n++
		}
	}
	hosts = hosts[:n]
	hosts = append(hosts, srv)

	cluster.Conf.AppHosts = strings.Join(hosts, ",")
	cluster.Lock()
	cluster.newAppList()
	cluster.Unlock()
	return nil
}
