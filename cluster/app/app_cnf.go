package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
)

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (app *App) LoadConfig() error {

	// Create a new configuration struct
	result := app.AppConfig

	filename := filepath.Join(app.Datadir, app.Name+".toml")

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
	app.AppConfig = result

	return nil
}

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (app *App) LoadDeploymentsConfig() error {
	// Create a new configuration struct
	var result map[string]config.AppSectionConfig

	filename := filepath.Join(app.Datadir, "deployments.json")

	// Load the configuration file
	_, err := os.Stat(filename)
	if err != nil {
		return err
	}

	// Open JSON file
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Decode JSON file into the configuration struct
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&result)
	if err != nil {
		// If there is an error decoding the JSON file don't change the configuration
		return err
	}

	app.DeployConfigMap = result

	return nil
}
