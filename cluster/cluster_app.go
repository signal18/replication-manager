package cluster

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
		Deployment:        new(config.Deployment),
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
	appcnf.Deployment = new(config.Deployment)

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

	t.WriteTo(file)

	if templatePath != "" {
		parentDir := filepath.Dir(templatePath)
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			os.MkdirAll(parentDir, os.ModePerm)
		}
		tfile, err := os.OpenFile(templatePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", templatePath)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
			}
			return false, err
		}
		defer tfile.Close()

		t.WriteTo(tfile)
	}

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

func (cluster *Cluster) AddSeededApp(srv, port, dockerImg, template string) error {
	var newViper *viper.Viper
	var content []byte
	var err error
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

	for _, app := range cluster.Conf.Apps {
		if app.AppHost == srv && app.AppPort == port {
			return errors.New("App already exists. If you want to add new deployment, please use the app deployment menu")
		}
	}

	appcnf := cluster.GetAppConfig(srv, port) // Get or initiate app config
	appcnf.ProvAppDockerImg = dockerImg
	appcnf.ProvAppTemplate = template
	cluster.Conf.Apps = append(cluster.Conf.Apps, appcnf)

	cluster.Lock()
	cluster.newAppList()
	cluster.Unlock()

	app := cluster.GetAppByConfig(appcnf)
	if app != nil {
		app.CheckPrimaryRoute()
	}

	if template != "" {
		resolvedContent, _ := cluster.ParseTemplateContent(app, content)
		newViper, err = cluster.LoadTemplateToViper(resolvedContent)
		newViper.Set("app-host", srv)
		newViper.Set("app-port", port)
		newViper.Set("prov-app-docker-img", dockerImg)
		newViper.Set("prov-app-template", template)

		// Unmarshal the parsed content into the app configuration
		err = newViper.Unmarshal(appcnf)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error unmarshalling parsed template file %s: %s", template, err)
			return err
		}
	}
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
	templateName := strings.TrimLeft(template, "shared/")
	templatePath := "app/deployments/" + templateName + ".toml"

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
	localPath := filepath.Join(cluster.Conf.WorkingDir, ".templates", "apps", template+".toml")

	// Try local file
	if content, err := cluster.loadLocalTemplate(localPath, template); err == nil {
		return content, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// Ensure parent dir exists
	if err := os.MkdirAll(filepath.Dir(localPath), os.ModePerm); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error creating parent directory for %s: %s", localPath, err)
		return nil, err
	}

	// Try repo
	content, err := cluster.loadTemplateFromRepo(template)
	if err != nil {
		// Fallback: shared dir
		content, err = cluster.loadTemplateFromShared(template)
		if err != nil {
			return nil, err
		}
	}

	// Cache locally
	if err := os.WriteFile(localPath, content, 0644); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error writing local template file %s: %s", localPath, err)
		return nil, err
	}

	return content, nil
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
	var parsed string = string(content)
	if app.AppClusterSubstitute != "" {
		parsed, err = cluster.ParseAppTemplate(string(content), app.AppClusterSubstitute)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error parsing template file: %s", err)
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

	if app.HasTemplateMD5Diff() {
		if app.HasProvisionCookie() {
			app.SetReprovCookie()
		}
	} else {
		app.DelReprovisionCookie()
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
	}
}

// InitiateRefreshTemplateMD5Worker starts a worker to refresh app template MD5 hashes.
// It exits cleanly when the channel is closed.
func (cluster *Cluster) InitiateRefreshTemplateMD5Worker(length int) {
	cluster.RefreshTemplateMD5Chan = make(chan *App, length)

	go func() {
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
	}()
}

func (cluster *Cluster) CloseRefreshTemplateMD5Worker() {
	close(cluster.RefreshTemplateMD5Chan)
}

func (cluster *Cluster) ReinitializeRefreshTemplateMD5Worker(length int) {
	cluster.CloseRefreshTemplateMD5Worker()
	cluster.InitiateRefreshTemplateMD5Worker(length)
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
