package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	vault "github.com/hashicorp/vault/api"
)

func (cluster *Cluster) GetVaultAdminConnection() (*vault.Client, error) {
	if cluster.Conf.IsVaultUsed() {

		vconfig := vault.DefaultConfig()
		vconfig.Address = cluster.Conf.VaultServerAddr
		client, err := vault.NewClient(vconfig)
		if err != nil {
			return nil, err
		}

		adminToken := cluster.Conf.GetDecryptedPassword("vault-admin-token", cluster.Conf.VaultAdminToken)
		if adminToken == "" {
			return nil, fmt.Errorf("vault admin token is required for admin operations")
		}

		client.SetToken(adminToken)
		_, err = client.Auth().Token().LookupSelf()
		if err != nil {
			return nil, fmt.Errorf("failed to lookup vault token: %w", err)
		}

		return client, nil
	}
	return nil, errors.New("Not using Vault")
}

func (cluster *Cluster) ListMounts() (map[string]*vault.MountOutput, error) {
	if !cluster.Conf.IsVaultUsed() {
		return nil, errors.New("Not using Vault")
	}

	adminClient := cluster.VaultAdminClient
	if adminClient == nil {
		adminClient, err := cluster.GetVaultAdminConnection()
		if err != nil {
			return nil, err
		}
		cluster.VaultAdminClient = adminClient
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.VaultTimeout)*time.Second)
	defer cancel()

	mounts, err := adminClient.Sys().ListMountsWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list mounts: %w", err)
	}

	return mounts, nil
}

func (cluster *Cluster) ListSecretConfigs(mountpath string) (*vault.Secret, error) {
	if !cluster.Conf.IsVaultUsed() {
		return nil, errors.New("Not using Vault")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.VaultTimeout)*time.Second)
	defer cancel()

	adminClient := cluster.VaultAdminClient
	if adminClient == nil {
		adminClient, err := cluster.GetVaultAdminConnection()
		if err != nil {
			return nil, err
		}
		cluster.VaultAdminClient = adminClient
	}

	secretConfigs, err := adminClient.Logical().ListWithContext(ctx, mountpath)
	if err != nil {
		return nil, fmt.Errorf("failed to list secret configs: %w", err)
	}

	return secretConfigs, nil
}

func (cluster *Cluster) MountDatabaseEngine() error {
	// Currently no action is needed to mount the database secrets engine
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("Not using Vault")
	}

	adminClient := cluster.VaultAdminClient
	if adminClient == nil {
		adminClient, err := cluster.GetVaultAdminConnection()
		if err != nil {
			return err
		}
		cluster.VaultAdminClient = adminClient
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.VaultTimeout)*time.Second)
	defer cancel()

	return adminClient.Sys().MountWithContext(ctx, "database", &vault.MountInput{Type: "database"})
}

func (cluster *Cluster) CreateDBVaultConfig() error {
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("Not using Vault")
	}

	if len(cluster.Proxies) == 0 {
		return errors.New("Unable to create database vault config without proxy")
	}

	// Get the host from a url
	parsedURL, err := url.Parse(cluster.Conf.VaultServerAddr)
	if err != nil {
		return fmt.Errorf("failed to parse vault server address: %w", err)
	}
	parsedHost := parsedURL.Hostname()

	adminClient, err := cluster.GetVaultAdminConnection()
	if err != nil {
		return err
	}

	mounts, err := cluster.ListMounts()
	if err != nil {
		return fmt.Errorf("failed to list vault mounts: %w", err)
	}

	if _, ok := mounts["database/"]; !ok {
		if cluster.Conf.VaultAutoMount {
			err = cluster.MountDatabaseEngine()
			if err != nil {
				return fmt.Errorf("failed to mount database engine: %w", err)
			}
		} else {
			return fmt.Errorf("database secrets engine is not mounted")
		}
	}

	list, err := cluster.ListSecretConfigs("database/config")
	if err != nil {
		return fmt.Errorf("failed to list database vault configs: %w", err)
	}

	if len(list.Data) > 0 {
		if _, ok := list.Data["keys"].([]interface{}); ok {
			for _, key := range list.Data["keys"].([]interface{}) {
				if key.(string) == cluster.Name {
					return errors.New("Database vault config already exists")
				}
			}
		}
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("Unable to find master server for database vault config")
	}

	vuser := cluster.Conf.VaultDBUser
	vpass, _ := cluster.GeneratePassword()

	// Make sure 'vaultuser'@'%' or 'vaultuser'@'<host>' does not already exist to avoid conflicts
	_, ok := master.Users.CheckAndGet("'" + vuser + "'@'%'")
	if ok {
		return fmt.Errorf("Unable to create database vault config: '%s'@'%' user already exists", vuser)
	}

	_, ok = master.Users.CheckAndGet("'" + vuser + "'@'" + parsedHost + "'")
	if ok {
		return fmt.Errorf("Unable to create database vault config: '%s'@'%s' user already exists", vuser, parsedHost)
	}

	dbprx, dbconn, err := cluster.GetWritableProxy()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.VaultTimeout)*time.Second)

	// Create 'vaultuser'@'%' with necessary privileges
	_, err = dbconn.ExecContext(ctx, "CREATE USER IF NOT EXISTS '"+vuser+"'@'%' IDENTIFIED BY '"+vpass+"'")
	if err != nil {
		cancel()
		dbconn.Close()
		return fmt.Errorf("failed to create database user: %w", err)
	}
	cancel()
	dbconn.Close()

	// Prepare database config data
	dbConfigData := map[string]interface{}{
		"plugin_name": "mysql-database-plugin",
		"connection_url": fmt.Sprintf("{{username}}:{{password}}@tcp(%s:%d)/",
			dbprx.GetHost(),
			dbprx.GetReadWritePort()),
		"username": cluster.Conf.GetDecryptedPassword("vault-user", cluster.Conf.VaultDBUser),
		"password": cluster.Conf.GetDecryptedPassword("vault-password", vpass),
	}

	ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cluster.Conf.VaultTimeout)*time.Second)
	defer cancel()

	_, err = adminClient.Logical().WriteWithContext(ctx, fmt.Sprintf("database/config/%s", cluster.Name), dbConfigData)
	if err != nil {
		return fmt.Errorf("failed to create database vault config: %w", err)
	}

	return nil
}
