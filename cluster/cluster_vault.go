package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) HasVaultAdminCredentials() bool {
	if cluster.Conf.VaultAuth == string(config.VaultAuthAppRole) {
		if cluster.Conf.VaultAdminRoleId != "" && cluster.Conf.VaultAdminSecretId != "" {
			return true
		}
	} else if cluster.Conf.VaultAuth == string(config.VaultAuthToken) {
		if cluster.Conf.VaultAdminToken != "" {
			return true
		}
	}

	return false
}

func (cluster *Cluster) VaultLogin(client *vault.Client, admin bool) error {
	var err error
	var authMethod vault.AuthMethod

	switch config.VaultAuth(cluster.Conf.VaultAuth) {
	case config.VaultAuthAppRole:
		var roleID, secretid string

		if admin {
			roleID = cluster.Conf.VaultAdminRoleId
			secretid = cluster.Conf.GetDecryptedPassword("vault-admin-secret-id", cluster.Conf.VaultAdminSecretId)
		} else {
			roleID = cluster.Conf.VaultRoleId
			secretid = cluster.Conf.GetDecryptedPassword("vault-secret-id", cluster.Conf.VaultSecretId)
		}

		secretID := &auth.SecretID{FromString: secretid}
		if roleID == "" || secretID == nil {
			return err
		}

		authMethod, err = auth.NewAppRoleAuth(
			roleID,
			secretID,
		)

		if err != nil {
			return err
		}

		authInfo, err := client.Auth().Login(context.Background(), authMethod)
		if err != nil {
			return err
		}
		if authInfo == nil {
			return err
		}
	case config.VaultAuthToken:
		var authToken string
		if admin {
			authToken = cluster.Conf.GetDecryptedPassword("vault-admin-token", cluster.Conf.VaultAdminToken)
		} else {
			authToken = cluster.Conf.GetDecryptedPassword("vault-token", cluster.Conf.VaultToken)
		}
		client.SetToken(authToken)
		authInfo, err := client.Auth().Token().LookupSelf()
		if err != nil {
			return fmt.Errorf("failed to lookup vault token: %w", err)
		}
		if authInfo == nil {
			return fmt.Errorf("invalid vault token")
		}
	case config.VaultAuthUserPass, config.VaultAuthLDAP, config.VaultAuthGithub, config.VaultAuthAliCloud, config.VaultAuthAWS, config.VaultAuthAzure, config.VaultAuthGCP, config.VaultAuthKerberos, config.VaultAuthKubernetes, config.VaultAuthRadius:
		return fmt.Errorf("vault auth method %s not yet implemented", cluster.Conf.VaultAuth)
	default:
		return fmt.Errorf("unknown vault auth method: %s", cluster.Conf.VaultAuth)
	}

	return nil
}

func (cluster *Cluster) getVaultConnection(admin bool) (*vault.Client, error) {
	if cluster.Conf.IsVaultUsed() {
		vconfig := vault.DefaultConfig()

		vconfig.Address = cluster.Conf.VaultServerAddr

		client, err := vault.NewClient(vconfig)
		if err != nil {
			return nil, err
		}

		err = cluster.VaultLogin(client, admin)
		if err != nil {
			return nil, err
		}
		return client, err
	}
	return nil, errors.New("Not using Vault")
}

func (cluster *Cluster) GetVaultConnection() (*vault.Client, error) {
	if cluster.Conf.IsVaultUsed() {

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlDbg, "Vault Authentification using %s method", cluster.Conf.VaultAuth)

		client, err := cluster.getVaultConnection(false)
		if err != nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}

		cluster.CanConnectVault = true
		return client, err
	}
	return nil, errors.New("Not using Vault")
}

func (cluster *Cluster) GetVaultAdminConnection() (*vault.Client, error) {
	if cluster.Conf.IsVaultUsed() {

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlDbg, "Vault Admin Authentification using %s method", cluster.Conf.VaultAuth)

		client, err := cluster.getVaultConnection(true)
		if err != nil {
			cluster.SetState("ERR00102", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00102"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVaultAdmin = false
			cluster.errorConnectVaultAdmin = err
			return nil, err
		}

		cluster.CanConnectVaultAdmin = true

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
	// --- 1. Get master node ---
	master := cluster.GetMaster()
	if master == nil {
		return "", "", "", errors.New("unable to find master server for database vault config")
	}

	// --- 2. Prepare Vault user ---
	vuser := cluster.Conf.VaultDBUser
	if strings.ContainsAny(vuser, "'\"\\") {
		return "", "", "", fmt.Errorf("invalid characters in vault DB username %q", vuser)
	}

	// --- 3. Generate password (retry once) ---
	var vpass string
	var err error
	for i := 0; i < 2; i++ {
		vpass, err = cluster.GeneratePassword()
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", "", "", fmt.Errorf("failed to generate vault password after retry: %w", err)
	}

	// --- 4. Parse Vault host ---
	parsedURL, err := url.Parse(cluster.Conf.VaultServerAddr)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse vault server address: %w", err)
	}
	parsedHost := parsedURL.Hostname()

	// --- 5. Ensure no conflicting users ---
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'%'"); ok {
		return "", "", "", fmt.Errorf("vault user '%s'@'%%' already exists", vuser)
	}
	if _, ok := master.Users.CheckAndGet("'" + vuser + "'@'" + parsedHost + "'"); ok {
		return "", "", "", fmt.Errorf("vault user '%s'@'%s' already exists", vuser, parsedHost)
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

	// --- 2. Ensure Vault admin connection ---
	adminClient, err := cluster.GetVaultAdminConnection()
	if err != nil {
		return fmt.Errorf("failed to get vault admin connection: %w", err)
	}
	cluster.VaultAdminClient = adminClient

	// --- 3. Ensure database engine is mounted ---
	err = cluster.EnsureEngineMounted("database/", "database")
	if err != nil {
		return err // The error already detailed
	}

	// --- 4. Ensure no existing config ---
	exists, err := cluster.IsVaultConfigExists("database/config", cluster.Name)
	if err != nil {
		return err
	}

	if exists {
		return fmt.Errorf("database vault config for %q already exists", cluster.Name)
	}

	// --- 5. Prepare DB User for vault---
	vuser, vpass, _, err := cluster.PrepareVaultDBUser()
	if err != nil {
		return err
	}

	timeout := time.Duration(cluster.Conf.VaultTimeout) * time.Second

	// --- 6. Create DB user safely ---
	dbprx, dbconn, err := cluster.GetWritableProxy()
	if err != nil {
		return fmt.Errorf("failed to get writable proxy: %w", err)
	}
	defer dbconn.Close()

	if err := cluster.CreateVaultDBUser(dbconn, timeout, vuser, "%", vpass); err != nil {
		return err
	}

	// --- 7. Write Vault DB config ---
	if err := cluster.WriteVaultDBConfig(vuser, vpass, dbprx.GetHost(), dbprx.GetReadWritePort()); err != nil {
		return fmt.Errorf("failed to write vault DB config: %w", err)
	}

	return nil
}

func (cluster *Cluster) WriteVaultDBStaticRole(rolename string) error {
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("not using Vault")
	}

	if !cluster.Conf.VaultAutoGenerateRoles {
		return fmt.Errorf("vault auto-generate-roles is disabled")
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

	var roleUser string
	if rolename == "monitor" {
		roleUser = cluster.GetDbUser()
	} else if rolename == "replication" {
		roleUser = cluster.GetRplUser()
	} else {
		return fmt.Errorf("unknown role name %q for vault DB static role", rolename)
	}

	timeout := time.Duration(cluster.Conf.VaultTimeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	path := fmt.Sprintf("database/static-roles/%s-%s", cluster.Name, rolename)

	roleData := map[string]interface{}{
		"db_name":             cluster.Name,
		"username":            roleUser,
		"rotation_statements": []string{fmt.Sprintf("ALTER USER '{{name}}'@'%%' IDENTIFIED BY '{{password}}';")},
	}

	if cluster.Conf.VaultRotationPeriod != "" {
		roleData["rotation_period"] = cluster.Conf.VaultRotationPeriod
	} else if cluster.Conf.VaultRotationSchedule != "" {
		roleData["rotation_schedule"] = cluster.Conf.VaultRotationSchedule
	}

	if _, err := adminClient.Logical().WriteWithContext(ctx, path, roleData); err != nil {
		return fmt.Errorf("failed to write Vault DB static role at %q: %w", path, err)
	}

	return nil
}

func (cluster *Cluster) CreateVaultDBStaticRoles() error {
	if !cluster.Conf.IsVaultUsed() {
		return errors.New("not using Vault")
	}

	if !cluster.Conf.VaultAutoGenerateRoles {
		return fmt.Errorf("vault auto-generate-roles is disabled")
	}

	if err := cluster.WriteVaultDBStaticRole("monitor"); err != nil {
		return fmt.Errorf("failed to write vault DB static role 'monitor': %w", err)
	}

	if cluster.GetDbUser() != cluster.GetRplUser() {
		if err := cluster.WriteVaultDBStaticRole("replication"); err != nil {
			return fmt.Errorf("failed to write vault DB static role 'replication': %w", err)
		}
	}

	return nil
}

func (cluster *Cluster) GetVaultMonitorCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == config.VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().User)

		if err != nil {
			return "", "", err
		}
		user, pass := misc.SplitPair(secret.Data["db-servers-credential"].(string))
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().User)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultShardProxyCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == config.VaultConfigStoreV2 {
		user, pass := misc.SplitPair(cluster.Conf.Secrets["shardproxy-credential"].Value)
		if savedConf.IsPath(cluster.Conf.MdbsProxyCredential) {

			secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.Conf.MdbsProxyCredential)

			if err != nil {
				return "", "", err
			}
			user, pass = misc.SplitPair(secret.Data["shardproxy-credential"].(string))
		}

		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().MdbsProxyCredential)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultProxySQLCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == config.VaultConfigStoreV2 {
		user := cluster.Conf.Secrets["proxysql-user"].Value
		pass := cluster.Conf.Secrets["proxysql-password"].Value
		if savedConf.IsPath(cluster.Conf.ProxysqlUser) {
			secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.Conf.ProxysqlUser)

			if err != nil {
				return "", "", err
			}
			user = secret.Data["proxysql-user"].(string)
		}

		if savedConf.IsPath(cluster.Conf.ProxysqlPassword) {
			secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().ProxysqlPassword)
			if err != nil {
				return "", "", err
			}
			pass = secret.Data["proxysql-password"].(string)
		}

		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().ProxysqlPassword)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultReplicationCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == config.VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().RplUser)

		if err != nil {
			return "", "", err
		}
		user, pass := misc.SplitPair(secret.Data["replication-credential"].(string))
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().RplUser)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) RotateVaultDatabaseStaticRoles() error {
	if cluster.Conf.VaultMode != config.VaultDbEngine {
		return nil
	}

	client, err := cluster.GetVaultConnection()
	if err != nil {
		return fmt.Errorf("failed to get vault connection: %w", err)
	}

	var mrole, rrole string

	if cluster.Conf.IsPath(cluster.Conf.User) {
		mrole = strings.TrimPrefix(cluster.Conf.User, "database/static-creds")
	} else {
		mrole = cluster.GetDbUser() // backward compatibility
	}
	mrole = strings.ReplaceAll("database/rotate-role/"+mrole, "//", "/")

	// Rotate monitor role
	err = client.KVv1("").Put(context.Background(), mrole, nil)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
	}

	// Rotate replication role if different
	if cluster.GetDbUser() != cluster.GetRplUser() {
		// Replication role rotation
		if cluster.Conf.IsPath(cluster.Conf.RplUser) {
			rrole = strings.TrimPrefix(cluster.Conf.RplUser, "database/static-creds")
		} else {
			rrole = cluster.GetRplUser()
		}
		rrole = strings.ReplaceAll("database/rotate-role/"+rrole, "//", "/")

		err = client.KVv1("").Put(context.Background(), rrole, nil)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "unable to rotate passwords for %s static role: %v", cluster.GetRplUser(), err)
		}
	}

	return nil
}
