package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/viper"
)

func (cluster *Cluster) NewAppConfig(apphost, port string) *config.AppConfig {
	return &config.AppConfig{
		AppHost:           apphost,
		AppPort:           port,
		ProvAppDiskType:   "volume",
		ProvAppMem:        cluster.GetAppMemory(nil),
		ProvAppCpuCores:   cluster.GetAppCores(nil),
		ProvAppDisk:       cluster.GetAppDisk(nil),
		ProvAppAgents:     cluster.GetAppAgents(nil),
		ProvAppHATopology: cluster.GetAppHATopology(nil),
		Deployment:        config.NewDeploymentConfig(),
	}
}

func (cluster *Cluster) LoadAppConfigs() error {
	dirname := filepath.Join(cluster.WorkingDir, "apps")

	// Check if the directory exists
	_, err := os.Stat(dirname)
	if os.IsNotExist(err) {
		// Create the directory if it does not exist
		err = os.MkdirAll(cluster.WorkingDir+"/apps", 0750)
		if err != nil {
			return err
		}
	}

	// Set the new configuration
	if cluster.Conf.Apps == nil {
		cluster.Conf.Apps = make([]*config.AppConfig, 0)
	}

	// Walk through the directory and load all the configuration files
	var firstErr error
	failedCount := 0
	walkErr := filepath.WalkDir(dirname, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".toml" {
			appname := strings.TrimSuffix(filepath.Base(path), ".toml")
			if loadErr := cluster.LoadAppConfig(dirname, appname); loadErr != nil {
				// ParseConfigMeasurement warnings are non-fatal and were historically ignored.
				var parseErrs config.ErrorConfigs
				if errors.As(loadErr, &parseErrs) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
						"App config %q loaded with measurement warnings: %v", path, loadErr)
					return nil
				}
				failedCount++
				if firstErr == nil {
					firstErr = loadErr
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
					"Failed to load app config %q: %v", path, loadErr)
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if failedCount > 0 {
		return fmt.Errorf("failed to load %d app config file(s): %w", failedCount, firstErr)
	}
	return nil
}

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (cluster *Cluster) LoadAppConfig(dirname, appname string) error {

	// Create a new configuration struct
	var appcnf config.AppConfig
	appcnf.Deployment = config.NewDeploymentConfig()

	filename := filepath.Join(dirname, appname+".toml")

	// Load the configuration file
	_, err := os.Stat(filename)
	if err != nil {
		return err
	}

	rawContent, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	canonicalContent, canonicalRes, err := config.CanonicalizeAppTemplateTOML(rawContent)
	if err != nil {
		return err
	}

	// Open TOML file
	appViper := viper.New()
	appViper.SetConfigType("toml")
	err = appViper.ReadConfig(bytes.NewBuffer(canonicalContent))
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

	if appcnf.Deployment != nil {
		if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
			for _, resolveErr := range resolveErrs {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
					"App config %q deployment path resolution error: %v", filename, resolveErr)
			}
			return fmt.Errorf("invalid deployment path mapping in app config %q", filename)
		}
	}

	if canonicalRes.Changed {
		t, err := toml.LoadBytes(canonicalContent)
		if err != nil {
			return err
		}
		if err := cluster.writeTomlAtomically(t, filename); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"Canonicalized legacy app config template in %q", filename)
	}

	// If app-host was not set in the TOML file (or was left as an unresolved template),
	// fall back to the file name so the app gets a valid, stable Name and ID.
	if appcnf.AppHost == "" || strings.Contains(appcnf.AppHost, "{{") {
		appcnf.AppHost = appname
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"App config %q had no resolved app-host; using filename as host: %s", filename, appname)
	}
	if appcnf.AppPort == "" {
		appcnf.AppPort = "80"
	}

	// Skip duplicate entries (same host+port already loaded, e.g. from main config).
	for _, existing := range cluster.Conf.Apps {
		if existing.AppHost == appcnf.AppHost && existing.AppPort == appcnf.AppPort {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg,
				"App config %s:%s already loaded, skipping duplicate from %q", appcnf.AppHost, appcnf.AppPort, filename)
			return nil
		}
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
		changed, err := cluster.SaveApp(app, "")
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

func (cluster *Cluster) SaveApp(app *App, templatePath string) (bool, error) {
	var has_changed bool
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	filePath := cluster.WorkingDir + "/apps/" + app.Name + ".toml"

	// Save the main configuration file
	changed, err := cluster.SaveAppConfigFile(app, filePath, templatePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
		return false, err
	}

	if changed {
		has_changed = true
	}

	return has_changed, nil
}

func (cluster *Cluster) SaveAppConfigFile(app *App, filePath, templatePath string) (bool, error) {

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

	if err := cluster.writeTomlAtomically(t, filePath); err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing app file atomically: %s", err)
		}
		return false, err
	}

	if templatePath != "" {
		parentDir := filepath.Dir(templatePath)
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			if err := os.MkdirAll(parentDir, 0750); err != nil {
				return false, err
			}
		}
		if err := cluster.writeTomlAtomically(t, templatePath); err != nil {
			if os.IsPermission(err) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", templatePath)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing template file atomically: %s", err)
			}
			return false, err
		}
	}

	return true, nil
}

// writeTomlAtomically writes a TOML tree via temp-file + fsync + rename to avoid
// truncating a target file on partial writes.
func (cluster *Cluster) writeTomlAtomically(t *toml.Tree, filePath string) error {
	parentDir := filepath.Dir(filePath)
	// 0750: owner rwx, group rx, other none — more restrictive than os.ModePerm
	// (0777) to protect config dirs that may hold database credentials.
	if err := os.MkdirAll(parentDir, 0750); err != nil {
		return err
	}

	perm := os.FileMode(0666)
	if fi, err := os.Stat(filePath); err == nil {
		perm = fi.Mode()
	}

	tmpFile, err := os.CreateTemp(parentDir, ".repman-toml-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := t.WriteTo(tmpFile); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}

	if dir, err := os.Open(parentDir); err == nil {
		if syncErr := dir.Sync(); syncErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"writeTomlAtomically: directory fsync failed for %s: %v (rename durability not guaranteed)", parentDir, syncErr)
		}
		if closeErr := dir.Close(); closeErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"writeTomlAtomically: directory close failed for %s: %v", parentDir, closeErr)
		}
	}

	return nil
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

func (cluster *Cluster) AddSeededApp(srv, port, dockerImg, template string) error {
	var newViper *viper.Viper
	var content []byte
	var err error

	srv = strings.TrimSpace(srv)
	port = strings.TrimSpace(port)
	dockerImg = strings.TrimSpace(dockerImg)
	template = strings.TrimSpace(template)

	if srv == "" {
		return errors.New("app host is required")
	}

	if dockerImg == "" && template == "" {
		return errors.New("docker image or template is required")
	}

	if template != "" {
		content, err = cluster.GetTemplateContent(template)
		if err != nil {
			return err
		}

		newViper, err = cluster.LoadTemplateToViper(content)
		if err != nil {
			return err
		}

		if port == "" || port == "0" {
			port = newViper.GetString("app-port")
			if port == "" || port == "0" {
				port = "80"
			}
		}

		if dockerImg == "" {
			dockerImg = newViper.GetString("prov-app-docker-img")
			if dockerImg == "" {
				return errors.New("Docker image is required in the template")
			}
		}
	}

	if port == "" || port == "0" {
		return errors.New("app port is required")
	}

	portNumber, convErr := strconv.Atoi(port)
	if convErr != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("app port must be between 1 and 65535")
	}

	for _, app := range cluster.Conf.Apps {
		if app.AppHost == srv && app.AppPort == port {
			return errors.New("App already exists. If you want to add new deployment, please use the app deployment menu")
		}
	}

	appcnf := cluster.GetAppConfig(srv, port) // Get or initiate app config
	appcnf.ProvAppDockerImg = dockerImg
	appcnf.ProvAppTemplate = template
	cluster.Conf.Apps = append(cluster.Conf.Apps, appcnf)
	appAdded := true
	rollbackAddedApp := func() {
		if !appAdded {
			return
		}
		for i, cnf := range cluster.Conf.Apps {
			if cnf == appcnf || (cnf.AppHost == srv && cnf.AppPort == port) {
				cluster.Conf.Apps = append(cluster.Conf.Apps[:i], cluster.Conf.Apps[i+1:]...)
				break
			}
		}
		cluster.Lock()
		_ = cluster.newAppList()
		cluster.Unlock()
		appAdded = false
	}

	cluster.Lock()
	if err := cluster.newAppList(); err != nil {
		cluster.Unlock()
		rollbackAddedApp()
		return err
	}
	cluster.Unlock()

	app := cluster.GetAppByConfig(appcnf)
	if app == nil {
		rollbackAddedApp()
		return fmt.Errorf("failed to create app object for %s:%s", srv, port)
	}
	app.CheckPrimaryRoute()

	if template != "" {
		resolvedContent, err := cluster.ParseTemplateContent(app, content)
		if err != nil {
			rollbackAddedApp()
			return err
		}

		canonicalContent, _, err := config.CanonicalizeAppTemplateTOML(resolvedContent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Error canonicalizing parsed template content for %s: %s", template, err)
			rollbackAddedApp()
			return err
		}

		newViper, err = cluster.LoadTemplateToViper(canonicalContent)
		if err != nil {
			rollbackAddedApp()
			return err
		}
		newViper.Set("app-host", srv)
		newViper.Set("app-port", port)
		newViper.Set("prov-app-docker-img", dockerImg)
		newViper.Set("prov-app-template", template)

		// Unmarshal the parsed content into the app configuration
		err = newViper.Unmarshal(appcnf)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error unmarshalling parsed template file %s: %s", template, err)
			rollbackAddedApp()
			return err
		}

		if appcnf.Deployment != nil {
			if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
				for _, resolveErr := range resolveErrs {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
						"App template %q deployment path resolution error: %v", template, resolveErr)
				}
				rollbackAddedApp()
				return fmt.Errorf("invalid deployment path mapping for template %q", template)
			}
		}
	}
	appAdded = false
	return nil
}

func (cluster *Cluster) GetAppByHostPort(host, port string) (*App, int) {
	// Check if the app exists in the cluster
	for i, app := range cluster.Apps {
		if app.GetHost() == host && app.GetPort() == port {
			return app, i // Return the existing app and its index
		}
	}

	return nil, -1
}

func (cluster *Cluster) GetAppByConfig(appcnf *config.AppConfig) *App {
	// Check if the app exists in the cluster
	for _, app := range cluster.Apps {
		if app.AppConfig != nil && app.AppConfig.AppHost == appcnf.AppHost && app.AppConfig.AppPort == appcnf.AppPort {
			return app
		}
	}

	return nil
}

func (cluster *Cluster) GetAppAgents(appcnf *config.AppConfig) string {
	if appcnf != nil {
		// Get the app config
		if appcnf.ProvAppAgents != "" {
			// If the app config has agents, return them
			return appcnf.ProvAppAgents
		}
	}

	// If the app config does not have agents, return the cluster agents
	agents := cluster.Conf.ProvAppAgents
	if agents == "" {
		// If the cluster does not have agents, return the default agents
		agents = cluster.Conf.ProvAgents
	}

	if agents != "" && appcnf != nil {
		appcnf.ProvAppAgents = agents
	}

	return agents
}

func (cluster *Cluster) GetAppDisk(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppDisk != "" {
		// If the app config has disk, return it
		return appcnf.ProvAppDisk
	}

	// If the app config does not have disk, return the cluster disk
	disk := cluster.Conf.ProvAppDisk
	if disk == "" {
		// If the cluster does not have disk, return the default disk
		disk = cluster.Conf.ProvDisk
	}

	if disk != "" && appcnf != nil {
		appcnf.ProvAppDisk = disk
	}

	return disk
}

func (cluster *Cluster) GetAppMemory(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppMem != "" {
		// If the app config has memory, return it
		return appcnf.ProvAppMem
	}

	// If the app config does not have memory, return the cluster memory
	mem := cluster.Conf.ProvAppMem
	if mem == "" {
		// If the cluster does not have memory, return the default memory
		mem = cluster.Conf.ProvMem
	}

	if mem != "" && appcnf != nil {
		appcnf.ProvAppMem = mem
	}

	return mem
}

// GetAppCores returns the cores for the app.
func (cluster *Cluster) GetAppCores(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppCpuCores != "" {
		// If the app config has cores, return it
		return appcnf.ProvAppCpuCores
	}

	// If the app config does not have cores, return the cluster cores
	cores := cluster.Conf.ProvAppCpuCores
	if cores == "" {
		// If the cluster does not have cores, return the default cores
		cores = cluster.Conf.ProvCores
	}

	if cores != "" && appcnf != nil {
		appcnf.ProvAppCpuCores = cores
	}

	return cores
}

func (cluster *Cluster) GetAppHATopology(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppHATopology != "" {
		// If the app config has HA topology, return it
		return appcnf.ProvAppHATopology
	}

	// If the app config does not have HA topology, return the cluster HA topology
	haTopology := cluster.Conf.ProvAppHATopology
	if haTopology != "" && appcnf != nil {
		appcnf.ProvAppHATopology = haTopology
	}

	return haTopology
}

func (cluster *Cluster) refreshApps(wg *sync.WaitGroup) {
	defer wg.Done()

	var workerWg sync.WaitGroup
	appChan := make(chan *App)

	workerCount := cluster.Conf.AppRefreshConcurrency
	if workerCount <= 0 {
		workerCount = 1
	}

	for range workerCount {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for app := range appChan {
				app.Refresh()
			}
		}()
	}

	for _, app := range cluster.Apps {
		if app != nil {
			appChan <- app
		}
	}
	close(appChan)

	workerWg.Wait()
}

func (cluster *Cluster) EmitAppErrors() {
	for _, app := range cluster.Apps {
		if app == nil {
			continue
		}
		app.Lock()
		for key, st := range app.ErrState {
			if st.ErrKey == "" {
				st.ErrKey = key
			}

			cluster.SetState(key, st)
		}
		app.Unlock()
	}
}

func (cluster *Cluster) GetAppByURL(url string) (*App, int) {
	var host, port string
	newURL := strings.TrimSpace(url)
	if newURL == "" {
		return nil, -1 // Return nil if the URL is empty
	}

	// Split the URL and strip the protocol if present
	if strings.Contains(newURL, "://") {
		parts := strings.SplitN(newURL, "://", 2)
		if len(parts) == 2 {
			newURL = parts[1] // Use the part after the protocol
		}
	}

	// Split the URL into host and port
	parts := strings.SplitN(newURL, ":", 2)
	if len(parts) == 2 {
		host = parts[0]
		port = parts[1]
	} else {
		host = parts[0]
		port = "80" // Default port if not specified
	}

	return cluster.GetAppByHostPort(host, port)
}

func (cluster *Cluster) GetAppDecryptedVariableValue(app *App, key string) (string, error) {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return "", errors.New("app or app configuration is not initialized")
	}

	for _, variable := range app.AppConfig.Deployment.Variables {
		if variable.Name == key {
			return cluster.Conf.GetDecryptedPassword(key, variable.Value), nil
		}
	}

	return "", errors.New("variable not found")
}

func (cluster *Cluster) GetAppEncryptedVariableValue(app *App, key string) (string, error) {
	decrypted, err := cluster.GetAppDecryptedVariableValue(app, key)
	if err != nil {
		return "", err
	}

	return cluster.Conf.GetEncryptedString(decrypted), nil
}

func (cluster *Cluster) SetAppVariableValue(app *App, v config.VariableMapping) error {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return errors.New("app or app configuration is not initialized")
	}
	newValue := v.Value
	if v.Type == config.VariableTypeSecret {
		newValue = cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedPassword(v.Name, v.Value))
	}

	for i, variable := range app.AppConfig.Deployment.Variables {
		if variable.Name == v.Name {
			app.AppConfig.Deployment.Variables[i].Value = newValue
			return nil
		}
	}

	// If the variable does not exist, add it
	app.AppConfig.Deployment.Variables = append(app.AppConfig.Deployment.Variables, config.VariableMapping{Name: v.Name, Value: newValue})
	return nil
}

func normalizeTemplateIdentifier(template string) (string, error) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", fmt.Errorf("template name must be provided")
	}

	cleanTemplateName := filepath.Clean(filepath.FromSlash(trimmed))
	if cleanTemplateName == "." || cleanTemplateName == string(filepath.Separator) {
		return "", fmt.Errorf("template name must be provided")
	}
	if filepath.IsAbs(cleanTemplateName) {
		return "", fmt.Errorf("template name must be relative to templates root")
	}

	relFromRoot, err := filepath.Rel(".", cleanTemplateName)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relFromRoot == ".." || strings.HasPrefix(relFromRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}

	normalizedTemplateName := filepath.ToSlash(cleanTemplateName)
	if normalizedTemplateName == "shared" {
		return "", fmt.Errorf("shared template name must include a template path")
	}
	return normalizedTemplateName, nil
}

func (cluster *Cluster) resolveTemplateLocalCachePath(template string) (string, string, error) {
	normalizedTemplateName, err := normalizeTemplateIdentifier(template)
	if err != nil {
		return "", "", err
	}

	localTemplatesRootAbs, err := filepath.Abs(filepath.Clean(filepath.Join(cluster.Conf.WorkingDir, ".templates", "apps")))
	if err != nil {
		return "", "", fmt.Errorf("invalid templates root path: %w", err)
	}

	localPathAbs, err := filepath.Abs(filepath.Clean(filepath.Join(localTemplatesRootAbs, filepath.FromSlash(normalizedTemplateName)+".toml")))
	if err != nil {
		return "", "", fmt.Errorf("invalid template path: %w", err)
	}
	relPath, err := filepath.Rel(localTemplatesRootAbs, localPathAbs)
	if err != nil {
		return "", "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("template name contains invalid path traversal")
	}

	return normalizedTemplateName, localPathAbs, nil
}

func resolveSharedTemplateRepoPath(normalizedTemplate string) (string, error) {
	if !strings.HasPrefix(normalizedTemplate, "shared/") {
		return "", fmt.Errorf("shared template path must start with shared/")
	}

	sharedTemplateName := strings.TrimPrefix(normalizedTemplate, "shared/")
	cleanShared := filepath.Clean(filepath.FromSlash(sharedTemplateName))
	if cleanShared == "." || cleanShared == string(filepath.Separator) {
		return "", fmt.Errorf("shared template name must include a template path")
	}
	relFromRoot, err := filepath.Rel(".", cleanShared)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relFromRoot == ".." || strings.HasPrefix(relFromRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}

	return filepath.ToSlash(filepath.Join("app", "deployments", filepath.ToSlash(cleanShared)+".toml")), nil
}

func (cluster *Cluster) loadLocalTemplate(path, template string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error reading local template file %s: %s", template, err)
	}
	return content, err
}

func (cluster *Cluster) loadTemplateFromRepo(template string) ([]byte, error) {
	var (
		gClient   githelper.GitClientInterface
		baseURL   string
		projectID string
		err       error
	)

	gitpass := cluster.Conf.GetDecryptedPassword("template-repo", cluster.Conf.ProvAppTemplateRepoPassword)

	if strings.Contains(cluster.Conf.ProvAppTemplateRepo, "github") {
		_, projectID, err = githelper.ParseGitHubURL(cluster.Conf.ProvAppTemplateRepo)
		if err == nil {
			gClient, err = githelper.NewGithubClient(gitpass)
		}
	} else {
		baseURL, projectID, err = githelper.ParseGitLabURL(cluster.Conf.ProvAppTemplateRepo)
		if err == nil {
			gClient, err = githelper.NewGitlabClient(baseURL, gitpass)
		}
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error creating git client for repo %s: %s", cluster.Conf.ProvAppTemplateRepo, err)
		return nil, err
	}

	content, err := gClient.DownloadFileFromRepo(
		projectID,
		cluster.Conf.ProvAppTemplateRepoUser,
		template+".toml",
		time.Duration(cluster.Conf.ProvAppTemplateRepoTimeout)*time.Second,
	)
	return content, err
}

func (cluster *Cluster) loadTemplateFromShared(template string) ([]byte, error) {
	templatePath, err := resolveSharedTemplateRepoPath(template)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error validating shared template path %s: %s", template, err)
		return nil, err
	}

	content, err := share.ReadFileFromSharedDir(
		cluster.Conf.WithEmbed,
		cluster.Conf.ShareDir,
		templatePath,
	)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error reading template file %s: %s", templatePath, err)
		return nil, err
	}
	return content, nil
}

func (cluster *Cluster) GetTemplateContent(template string) ([]byte, error) {
	return cluster.getTemplateContent(template, false)
}

// RefreshTemplateContent bypasses local cache and refreshes template content
// from repo/shared source, then overwrites local cache with validated canonical
// content.
func (cluster *Cluster) RefreshTemplateContent(template string) ([]byte, error) {
	return cluster.getTemplateContent(template, true)
}

func (cluster *Cluster) getTemplateContent(template string, forceRefresh bool) ([]byte, error) {
	normalizedTemplateName, localPath, err := cluster.resolveTemplateLocalCachePath(template)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error validating template path %s: %s", template, err)
		return nil, err
	}

	if !forceRefresh {
		// Try local file
		if content, err := cluster.loadLocalTemplate(localPath, normalizedTemplateName); err == nil {
			canonicalContent, canonicalRes, canonErr := config.CanonicalizeAppTemplateTOML(content)
			if canonErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
					"Error canonicalizing local template file %s: %s", localPath, canonErr)
				return nil, canonErr
			}
			if err := cluster.validateTemplateDeploymentPaths(canonicalContent, normalizedTemplateName); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
					"Invalid local template file %s after canonicalization: %s", localPath, err)
				return nil, err
			}
			if canonicalRes.Changed {
				t, err := toml.LoadBytes(canonicalContent)
				if err != nil {
					return nil, err
				}
				if err := cluster.writeTomlAtomically(t, localPath); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
						"Error writing canonical local template file %s: %s", localPath, err)
					return nil, err
				}
			}
			return canonicalContent, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	// Ensure parent dir exists
	if err := os.MkdirAll(filepath.Dir(localPath), 0750); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error creating parent directory for %s: %s", localPath, err)
		return nil, err
	}

	// Try repo
	content, err := cluster.loadTemplateFromRepo(normalizedTemplateName)
	if err != nil {
		// Fallback: shared dir
		content, err = cluster.loadTemplateFromShared(normalizedTemplateName)
		if err != nil {
			return nil, err
		}
	}

	canonicalContent, _, err := config.CanonicalizeAppTemplateTOML(content)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error canonicalizing template file %s: %s", normalizedTemplateName, err)
		return nil, err
	}
	if err := cluster.validateTemplateDeploymentPaths(canonicalContent, normalizedTemplateName); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Invalid template file %s after canonicalization: %s", normalizedTemplateName, err)
		return nil, err
	}

	// Cache locally
	t, err := toml.LoadBytes(canonicalContent)
	if err != nil {
		return nil, err
	}
	if err := cluster.writeTomlAtomically(t, localPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error writing local template file %s: %s", localPath, err)
		return nil, err
	}

	return canonicalContent, nil
}

func (cluster *Cluster) validateTemplateDeploymentPaths(content []byte, template string) error {
	appViper := viper.New()
	appViper.SetConfigType("toml")
	if err := appViper.ReadConfig(bytes.NewBuffer(content)); err != nil {
		return err
	}

	var appcnf config.AppConfig
	appcnf.Deployment = config.NewDeploymentConfig()
	if err := appViper.Unmarshal(&appcnf); err != nil {
		return err
	}

	if appcnf.Deployment == nil {
		return nil
	}
	if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
		for _, resolveErr := range resolveErrs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
				"Template %q deployment path resolution error: %v", template, resolveErr)
		}
		return fmt.Errorf("invalid deployment path mapping for template %q", template)
	}

	return nil
}

func (cluster *Cluster) LoadTemplateToViper(content []byte) (*viper.Viper, error) {
	if content == nil {
		return nil, errors.New("template content is empty")
	}

	// read parsed content (toml format) and merge it into the app configuration
	appViper := viper.New()
	appViper.SetConfigType("toml")
	err := appViper.ReadConfig(bytes.NewBuffer(content))
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error reading parsed template file: %s", err)
		return nil, err
	}

	return appViper, nil
}

func (cluster *Cluster) ParseTemplateContent(app *App, content []byte) ([]byte, error) {
	var err error

	if app.AppClusterSubstitute == "" {
		// If the app cluster substitute is empty, generate it
		app.AppClusterSubstitute, err = cluster.GetAppsSubstitutionJSon(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error getting app cluster substitute for %s:%s: %s", app.Host, app.Port, err)
		}
	}

	// If the app cluster substitute is still empty, use the template as is
	var parsed = string(content)
	if app.AppClusterSubstitute != "" {
		parsed, err = cluster.ParseAppTemplate(string(content), app.AppClusterSubstitute)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error parsing template file: %s", err)
			return []byte(parsed), err
		}
	}
	return []byte(parsed), nil
}

func (cluster *Cluster) RefreshAppTemplateMD5(app *App) error {
	if app.IsHashingTemplate {
		return nil
	}

	app.IsHashingTemplate = true
	defer func() {
		app.IsHashingTemplate = false
	}()

	// Get the current app template MD5
	res, err := cluster.OpenSVCGetAppTemplateV2(app)
	if err != nil {
		return err
	}

	app.TemplateMD5 = misc.GetMD5HashFromBytes(res)

	if app.TemplateMD5Prov != "" {
		if app.HasTemplateMD5Diff() {
			if app.HasProvisionCookie() {
				app.SetReprovCookie()
			}
		} else {
			app.DelReprovisionCookie()
		}
	}
	return nil
}

func (cluster *Cluster) RefreshAllAppTemplateMD5() {
	for _, app := range cluster.Apps {
		if app.IsHashingTemplate {
			continue
		}

		err := cluster.RefreshAppTemplateMD5(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not refresh app template MD5 for app %s:  %s ", app.GetId(), err)
		}
	}
}

func (cluster *Cluster) LoadAppTemplateMD5Provisioned(app *App) error {
	templatefile := filepath.Join(app.Datadir, "opensvc_template.json")
	_, err := os.Stat(templatefile)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(templatefile)
	if err != nil {
		return err
	}

	app.TemplateMD5Prov = misc.GetMD5HashFromBytes(content)
	return nil
}

func (cluster *Cluster) LoadAllAppTemplateMD5Provisioned() {
	for _, app := range cluster.Apps {
		err := cluster.LoadAppTemplateMD5Provisioned(app)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not load app template MD5 provisioned for app %s:  %s ", app.GetId(), err)
		}

		if cluster.RefreshTemplateMD5Chan == nil {
			cluster.RefreshTemplateMD5Chan = make(chan *App, 10)
		}
		cluster.EnqueueRefreshAppTemplateMD5(app)
	}
}

// InitiateRefreshTemplateMD5Worker starts a worker to refresh app template MD5 hashes.
// It exits cleanly when the channel is closed.
func (cluster *Cluster) CreateTemplateMD5Channel() {
	cluster.RefreshTemplateMD5Chan = make(chan *App, 10)
}

func (cluster *Cluster) InitiateRefreshTemplateMD5Worker() {
	for app := range cluster.RefreshTemplateMD5Chan {
		if app == nil {
			continue
		}

		if err := cluster.RefreshAppTemplateMD5(app); err != nil {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModOrchestrator,
				config.LvlErr,
				"Cannot refresh app template MD5 for app %s: %s",
				app.GetId(), err,
			)
		}
	}

	cluster.LogModulePrintf(
		cluster.Conf.Verbose,
		config.ConstLogModOrchestrator,
		config.LvlInfo,
		"RefreshTemplateMD5 worker stopped (channel closed)",
	)
}

func (cluster *Cluster) CloseRefreshTemplateMD5Worker() {
	close(cluster.RefreshTemplateMD5Chan)
}

func (cluster *Cluster) EnqueueRefreshAppTemplateMD5(app *App) {
	if app == nil {
		return
	}

	defer func() {
		_ = recover() // ignore panic if channel is closed
	}()

	select {
	case cluster.RefreshTemplateMD5Chan <- app:
		// Enqueued successfully
	default:
		// Channel full — drop silently
	}
}

func (cluster *Cluster) CheckAppsCredit() {
	for _, app := range cluster.Apps {
		if app != nil {
			app.CheckAppCredits()
		}
	}
}
