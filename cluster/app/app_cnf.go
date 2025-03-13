package app

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
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
	var result []config.Deployment

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

	app.Deployments = result

	return nil
}

func (app *App) Save() error {
	conf := app.Cluster.GetConf()
	_, file, no, ok := runtime.Caller(1)
	if ok {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	var has_changed bool

	// Save the main configuration file
	has_changed, err := app.SaveConfigFile()
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
		return err
	}

	// Checksum decrypted value to prevent unnecessary file
	new_ih, err := app.AppConfig.GetImmutableChecksum()
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during checksum immutable config: %s", err)
	}
	old_ih, ok := app.GetChecksumConfig("plain-immutable")

	new_sh, err := app.AppConfig.GetSecretChecksum()
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during checksum secret config: %s", err)
	}
	old_sh, ok2 := app.GetChecksumConfig("plain-secret")

	non_secret_change := !ok || !bytes.Equal(old_ih.Sum(nil), new_ih.Sum(nil))
	secret_change := !ok2 || !bytes.Equal(old_sh.Sum(nil), new_sh.Sum(nil))
	if non_secret_change {
		app.SetChecksumConfig("plain-immutable", new_ih)
	}

	if secret_change {
		app.SetChecksumConfig("plain-secret", new_sh)
	}

	// Only save if the value is changed
	if non_secret_change || secret_change {

		has_changed = true
		// Save the immutable configuration file
		_, err := app.SaveImmutableConfig()
		if err != nil {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app immutable config: %s", err)
			return err
		}

		// Save the cache configuration file
		if err := app.SaveCacheConfig(); err != nil {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app cache config: %s", err)
			return err
		}
	}

	_, err = app.Overwrite()
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
	}

	if has_changed {
		app.Cluster.SetIsNeedGitPush(true)
	}

	return nil
}

func (app *App) SaveConfigFile() (bool, error) {
	conf := app.Cluster.GetConf()
	var has_changed bool

	filePath := app.Datadir + "/" + app.Name + ".toml"
	header := "[saved-" + app.Name + "]\ntitle = \"" + app.Name + "\" \n"

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(app.AppConfig)
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
		return false, err
	}

	// Load TOML and sort keys
	t, err := toml.LoadBytes(readconf)
	if err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
		return false, err
	}

	s := t
	keys := t.Keys()
	keys = misc.SortKeysAsc(keys)

	// Write sorted values to file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
		}
		return false, err
	}
	defer file.Close()

	// Write header
	file.WriteString(header)

	for _, key := range keys {
		_, ok := app.AppConfig.ImmutableFlagMap[key]
		if ok {
			s.Delete(key)
		} else {
			v, ok := app.AppConfig.DefaultFlagMap[key]
			if ok && fmt.Sprintf("%v", s.Get(key)) == fmt.Sprintf("%v", v) {
				s.Delete(key)
			} else if !ok {
				s.Delete(key)
			} else if _, ok = conf.Secrets[key]; ok {
				s.Delete(key)
				//to encrypt credentials before writting in the config file
				encrypt_val := app.GetEncryptedValueFromMemory(key)
				file.WriteString(key + " = \"" + encrypt_val + "\"\n")

			}
		}
	}

	s.WriteTo(file)
	//fmt.Printf("SAVE APP IMMUABLE MAP : %s", conf.ImmuableFlagMap)
	//fmt.Printf("SAVE APP DYNAMIC MAP : %s", conf.DynamicFlagMap)
	new_h := md5.New()
	if _, err := io.Copy(new_h, file); err != nil {
		app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
	}

	h, ok := app.GetChecksumConfig("saved")
	if !ok {
		has_changed = true
	}
	if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
		has_changed = true
	}

	app.SetChecksumConfig("saved", new_h)

	return has_changed, nil
}

func (app *App) GetEncryptedValueFromMemory(key string) string {
	clusterConf := app.Cluster.GetConf()

	secretmap, ok := app.AppConfig.SecretMap[key]
	if !ok {
		return ""
	}

	total := len(secretmap)

	if total == 0 {
		return ""
	}

	if total == 1 {
		v, ok := secretmap[""]
		if ok {
			return clusterConf.GetEncryptedString(v.Value)
		}
	}

	secrets := make([]string, 0)

	for skey, sval := range secretmap {
		secrets = append(secrets, skey+":"+clusterConf.GetEncryptedString(sval.Value))
	}

	return strings.Join(secrets, ",")
}

func (app *App) SaveImmutableConfig() (bool, error) {
	clusterConf := app.Cluster.GetConf()
	var has_changed bool

	// Get Sorted Keys
	keys := make([]string, 0)
	for key, _ := range app.AppConfig.ImmutableFlagMap {
		keys = append(keys, key)
	}

	keys = misc.SortKeysAsc(keys)

	// Open file and
	file2, err := os.OpenFile(app.Datadir+"/immutable.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", clusterConf.WorkingDir+"/"+app.Name+"/immutable.toml")
		}
		return false, err
	}
	defer file2.Close()

	for _, key := range keys {
		val := app.AppConfig.ImmutableFlagMap[key]
		_, ok := app.AppConfig.SecretMap[key]
		if ok {
			encrypt_val := app.GetEncryptedValueFromMemory(key)
			file2.WriteString(key + " = \"" + encrypt_val + "\"\n")
		} else {
			if fmt.Sprintf("%T", val) == "string" {
				file2.WriteString(key + " = \"" + fmt.Sprintf("%v", val) + "\"\n")
			} else {
				file2.WriteString(key + " = " + fmt.Sprintf("%v", val) + "\n")
			}
		}
	}

	new_h := md5.New()
	if _, err := io.Copy(new_h, file2); err != nil {
		app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
	}

	h, ok := app.GetChecksumConfig("immutable")
	if !ok {
		has_changed = true
	}
	if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
		has_changed = true
	}

	app.SetChecksumConfig("immutable", new_h)

	return has_changed, nil
}

func (app *App) SaveCacheConfig() error {
	clusterConf := app.Cluster.GetConf()

	filePath := clusterConf.WorkingDir + "/" + app.Name + "/cache.toml"
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
		}
		return err
	}
	defer file.Close()

	keys := make([]string, 0)
	for key, _ := range app.AppConfig.ImmutableFlagMap {
		keys = append(keys, key)
	}

	keys = misc.SortKeysAsc(keys)

	for _, key := range keys {
		if _, ok := app.AppConfig.SecretMap[key]; ok {
			encrypt_val := app.GetEncryptedValueFromMemory(key)
			file.WriteString(key + " = \"" + encrypt_val + "\"\n")
		}
	}

	return nil
}

func (app *App) Overwrite() (bool, error) {
	clusterConf := app.Cluster.GetConf()

	var has_changed bool

	if clusterConf.ConfRewrite {
		file, err := os.OpenFile(clusterConf.WorkingDir+"/"+app.Name+"/overwrite.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", clusterConf.WorkingDir+"/"+app.Name+"/overwrite.toml")
			}
			return false, err
		}
		defer file.Close()

		readconf, _ := toml.Marshal(app.AppConfig)
		t, _ := toml.LoadBytes(readconf)
		s := t
		keys := t.Keys()
		keys = misc.SortKeysAsc(keys)

		for _, key := range keys {
			v, ok := app.AppConfig.ImmutableFlagMap[key]
			if !ok {
				s.Delete(key)
			} else {
				newsecret := app.GetSecretMapValue(key, false)
				oldsecret := app.GetSecretMapValue(key, true)
				if ok && fmt.Sprintf("%v", s.Get(key)) == fmt.Sprintf("%v", v) && (newsecret == oldsecret || oldsecret == "") {
					s.Delete(key)
				} else if _, ok = app.AppConfig.SecretMap[key]; ok && newsecret != v {
					v := app.GetEncryptedValueFromMemory(key)
					if v != "" {
						s.Set(key, v)
					} else {
						s.Delete(key)
					}
				}

			}

		}

		file.WriteString("[overwrite-" + app.Name + "]\n")
		s.WriteTo(file)

		new_h := md5.New()
		if _, err := io.Copy(new_h, file); err != nil {
			app.Cluster.LogModulePrintf(clusterConf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
		}

		h, ok := app.GetChecksumConfig("overwrite")
		if !ok {
			has_changed = true
		}
		if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
			has_changed = true
		}

		app.SetChecksumConfig("overwrite", new_h)

	}

	return has_changed, nil
}

func (app *App) GetSecretMapValue(key string, old bool) string {
	secretmap, ok := app.AppConfig.SecretMap[key]
	if !ok {
		return ""
	}

	total := len(secretmap)
	if total == 0 {
		return ""
	}

	if total == 1 {
		v, ok := secretmap[""]
		if ok {
			if old {
				return v.OldValue
			}
			return v.Value
		}
	}

	secrets := make([]string, 0)
	if old {
		for skey, sval := range secretmap {
			secrets = append(secrets, skey+":"+sval.OldValue)
		}
	} else {
		for skey, sval := range secretmap {
			secrets = append(secrets, skey+":"+sval.Value)
		}
	}

	return strings.Join(secrets, ",")
}

func (app *App) GetChecksumConfig(key string) (hash.Hash, bool) {
	h, ok := app.Cluster.GetChecksumConfig(app.Id + key)
	if !ok {
		return nil, ok
	}
	return h, ok
}

func (app *App) SetChecksumConfig(key string, value hash.Hash) {
	app.Cluster.SetChecksumConfig(app.Id+key, value)
}
