package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/jmoiron/sqlx"
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

func (cluster *Cluster) MountEngine(path, engineType string) error {

	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	adminClient, err := cluster.GetVaultAdminConnection()
	if err != nil {
		return fmt.Errorf("failed to get vault admin connection: %w", err)
	}

	mount := &vault.MountInput{
		Type: engineType,
	}

	if err := adminClient.Sys().Mount(path, mount); err != nil {
		return fmt.Errorf("failed to mount vault engine %q at %q: %w", engineType, path, err)
	}

	return nil
}

func (cluster *Cluster) EnsureEngineMounted(path, engineType string) error {
	mounts, err := cluster.ListMounts()
	if err != nil {
		return fmt.Errorf("failed to list vault mounts: %w", err)
	}

	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	// Already mounted
	if _, ok := mounts[path]; ok {
		return nil
	}

	// Attempt auto-mount if enabled
	if cluster.Conf.VaultAutoMount {
		if err := cluster.MountEngine(path, engineType); err != nil {
			return fmt.Errorf("failed to mount vault engine %q at %q: %w", engineType, path, err)
		}
		return nil
	}

	return fmt.Errorf("vault secrets engine %q is not mounted at %q and auto-mount is disabled", engineType, path)
}

func (cluster *Cluster) IsVaultConfigExists(path, name string) (bool, error) {
	list, err := cluster.ListSecretConfigs(path)
	if err != nil {
		return false, fmt.Errorf("failed to list vault configs at %q: %w", path, err)
	}

	rawKeys, ok := list.Data["keys"].([]interface{})
	if !ok {
		return false, nil // no keys → config doesn't exist
	}

	for _, key := range rawKeys {
		k, ok := key.(string)
		if !ok {
			continue // skip malformed entries
		}
		if k == name {
			return true, nil
		}
	}

	return false, nil
}

func (cluster *Cluster) PrepareVaultDBUser() (string, string, string, error) {
	master := cluster.GetMaster()
	if master == nil {
		return "", "", "", errors.New("Unable to find master server for database vault config")
	}

	vuser := cluster.Conf.VaultDBUser
	vpass, _ := cluster.GeneratePassword()

	parsedURL, err := url.Parse(cluster.Conf.VaultServerAddr)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse vault server address: %w", err)
	}
	parsedHost := parsedURL.Hostname()

	// Ensure no conflicting user exists
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'%'"); ok {
		return "", "", "", fmt.Errorf("Unable to create database vault config: '%s'@'%%' user already exists", vuser)
	}
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'" + parsedHost + "'"); ok {
		return "", "", "", fmt.Errorf("Unable to create database vault config: '%s'@'%s' user already exists", vuser, parsedHost)
	}

	return vuser, vpass, parsedHost, nil
}

func (cluster *Cluster) CreateVaultDBUser(dbconn *sqlx.DB, timeout time.Duration, username, host, password string) error {
	if strings.ContainsAny(username, "'\"\\") {
		return fmt.Errorf("invalid characters in username %q", username)
	}

	if strings.ContainsAny(host, "'\"\\") {
		return fmt.Errorf("invalid characters in host %q", host)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	query := fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY ?", username, host)

	if _, err := dbconn.ExecContext(ctx, query, password); err != nil {
		return fmt.Errorf("failed to create database user %q: %w", username, err)
	}

	return nil
}

func (cluster *Cluster) WriteVaultDBConfig(username, password, dbhost string, dbport int) error {
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("not using Vault")
	}

	adminClient := cluster.VaultAdminClient
	if adminClient == nil {
		client, err := cluster.GetVaultAdminConnection()
		if err != nil {
			return fmt.Errorf("failed to get Vault admin connection: %w", err)
		}
		adminClient = client
		cluster.VaultAdminClient = client
	}

	dbConfigData := map[string]interface{}{
		"plugin_name": "mysql-database-plugin",
		"connection_url": fmt.Sprintf(
			"{{username}}:{{password}}@tcp(%s:%d)/",
			dbhost, dbport,
		),
		"username": username,
		"password": password,
	}

	timeout := time.Duration(cluster.Conf.VaultTimeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	path := fmt.Sprintf("database/config/%s", cluster.Name)
	if _, err := adminClient.Logical().WriteWithContext(ctx, path, dbConfigData); err != nil {
		return fmt.Errorf("failed to write Vault DB config at %q: %w", path, err)
	}

	return nil
}

func (cluster *Cluster) CreateDBVaultConfig() error {
	// --- 1. Basic validation ---
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("not using Vault")
	}
	if len(cluster.Proxies) == 0 {
		return errors.New("unable to create database vault config without proxy")
	}

	// --- 2. Parse Vault host ---
	parsedURL, err := url.Parse(cluster.Conf.VaultServerAddr)
	if err != nil {
		return fmt.Errorf("failed to parse vault server address: %w", err)
	}
	parsedHost := parsedURL.Hostname()

	// --- 3. Ensure Vault admin connection ---
	adminClient, err := cluster.GetVaultAdminConnection()
	if err != nil {
		return fmt.Errorf("failed to get vault admin connection: %w", err)
	}
	cluster.VaultAdminClient = adminClient

	// --- 4. Ensure database engine is mounted ---
	err = cluster.EnsureEngineMounted("database/", "database")
	if err != nil {
		return err // The error already detailed
	}

	// --- 5. Ensure no existing config ---
	exists, err := cluster.IsVaultConfigExists("database/config", cluster.Name)
	if err != nil {
		return err
	}

	if exists {
		return fmt.Errorf("database vault config for %q already exists", cluster.Name)
	}

	// --- 6. Get master node ---
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("unable to find master server for database vault config")
	}

	// --- 7. Prepare Vault DB user credentials ---
	vuser := cluster.Conf.VaultDBUser
	var vpass string
	for i := 0; i < 2; i++ { // Try twice
		vpass, err = cluster.GeneratePassword()
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("failed to generate vault password after retry: %w", err)
	}

	timeout := time.Duration(cluster.Conf.VaultTimeout) * time.Second

	// --- 8. Ensure user does not already exist ---
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'%'"); ok {
		return fmt.Errorf("vault user '%s'@'%%' already exists", vuser)
	}
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'" + parsedHost + "'"); ok {
		return fmt.Errorf("vault user '%s'@'%s' already exists", vuser, parsedHost)
	}

	// --- 9. Create DB user safely ---
	dbprx, dbconn, err := cluster.GetWritableProxy()
	if err != nil {
		return fmt.Errorf("failed to get writable proxy: %w", err)
	}
	defer dbconn.Close()

	if err := cluster.CreateVaultDBUser(dbconn, timeout, vuser, "%", vpass); err != nil {
		return err
	}

	// --- 10. Write Vault DB config ---
	if err := cluster.WriteVaultDBConfig(vuser, vpass, dbprx.GetHost(), dbprx.GetReadWritePort()); err != nil {
		return fmt.Errorf("failed to write vault DB config: %w", err)
	}

	return nil
}
