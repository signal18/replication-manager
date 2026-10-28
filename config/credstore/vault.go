// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/signal18/replication-manager/config"
)

// The secret_kv-vault store (#2227): the one Vault code of repman. The primary Vault (the
// Vault* fields, the projection of the primary entry) and every Vault entry taking copies are
// a VaultKV each, built from their settings; nothing else talks to Vault.

// SourceVault is a credential read from Vault, shown in the GUI.
const SourceVault = "vault"

// VaultSettings is how a VaultKV reaches its Vault. SecretID is plain: it never leaves the
// store, never reaches a log (F10).
type VaultSettings struct {
	Address  string
	Mount    string // the KV v2 mount
	Mode     string // config.VaultConfigStoreV2 or config.VaultDbEngine
	Auth     string // config.ConstVaultAuthAppRole or config.ConstVaultAuthJWT
	RoleID   string
	SecretID string
	// JWT is the identity token of a jwt sign-in: the instance's Cloud18 identity (#2159)
	JWT func() (string, error)
}

// cloud18JWT is the instance's Cloud18 identity token.
func cloud18JWT(conf *config.Config) func() (string, error) {
	return func() (string, error) {
		token, _, err := conf.Cloud18IdentityToken()
		return token, err
	}
}

// VaultSettingsFromConfig are the settings of the primary Vault: the Vault* fields (for a
// cluster, the projection of its primary Vault entry; for the server, its own vault-* keys).
func VaultSettingsFromConfig(conf *config.Config) VaultSettings {
	return VaultSettings{
		Address:  conf.VaultServerAddr,
		Mount:    conf.VaultMount,
		Mode:     conf.VaultMode,
		Auth:     conf.VaultAuth,
		RoleID:   conf.VaultRoleId,
		SecretID: conf.GetDecryptedPassword("vault-secret-id", conf.VaultSecretId),
		JWT:      cloud18JWT(conf),
	}
}

// VaultSettingsFromEntry are the settings of a secret_kv-vault entry.
func VaultSettingsFromEntry(conf *config.Config, e config.SecretKvConfig) VaultSettings {
	return VaultSettings{
		Address:  conf.SecretKvSetting(e, config.ConstSecretKvVaultAddress),
		Mount:    conf.SecretKvSetting(e, config.ConstSecretKvVaultMount),
		Mode:     conf.SecretKvSetting(e, config.ConstSecretKvVaultMode),
		Auth:     conf.SecretKvSetting(e, config.ConstSecretKvVaultAuth),
		RoleID:   conf.SecretKvSetting(e, config.ConstSecretKvVaultRoleID),
		SecretID: conf.SecretKvSetting(e, config.ConstSecretKvVaultSecretID),
		JWT:      cloud18JWT(conf),
	}
}

// VaultKV is a Vault: its KV v2 (config_store_v2) or its database engine (database_engine).
// As a CredentialStore it answers the keys whose config value is a Vault path (path gives it).
type VaultKV struct {
	settings VaultSettings
	path     func(key string) (string, bool)
}

// NewVaultKV is the Vault of settings; path gives the Vault path a key's config value holds
// (nil: the store holds no key, it only takes copies).
func NewVaultKV(settings VaultSettings, path func(key string) (string, bool)) *VaultKV {
	return &VaultKV{settings: settings, path: path}
}

// NewPrimaryVault is the cluster's (or the server's) primary Vault: a key is held when its
// config value is a Vault path.
func NewPrimaryVault(conf *config.Config) *VaultKV {
	return NewVaultKV(VaultSettingsFromConfig(conf), func(key string) (string, bool) {
		v, ok := conf.Secrets.Lookup(key)
		if !ok || !conf.IsPath(v.Value) {
			return "", false
		}
		return v.Value, true
	})
}

// Settings are the store's settings.
func (v *VaultKV) Settings() VaultSettings { return v.settings }

// Connect signs in: approle (role id, secret id) or jwt (the identity token, the role being the
// role id, else the realm's client id).
func (v *VaultKV) Connect(ctx context.Context) (*vault.Client, error) {
	if v.settings.Address == "" {
		return nil, errors.New("Not using Vault")
	}
	cfg := vault.DefaultConfig()
	cfg.Address = v.settings.Address
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if v.settings.Auth == config.ConstVaultAuthJWT {
		return v.jwtLogin(ctx, client)
	}
	if v.settings.RoleID == "" || v.settings.SecretID == "" {
		return nil, errors.New("vault approle login: no role id or secret id")
	}
	appRoleAuth, err := auth.NewAppRoleAuth(v.settings.RoleID, &auth.SecretID{FromString: v.settings.SecretID})
	if err != nil {
		return nil, err
	}
	authInfo, err := client.Auth().Login(ctx, appRoleAuth)
	if err != nil {
		return nil, err
	}
	if authInfo == nil {
		return nil, errors.New("vault approle login: no auth info in the answer")
	}
	return client, nil
}

// jwtLogin logs in to the vault's jwt auth method with the instance's identity token (#2159):
// no secret of the vault is kept here.
func (v *VaultKV) jwtLogin(ctx context.Context, client *vault.Client) (*vault.Client, error) {
	if v.settings.JWT == nil {
		return nil, errors.New("vault jwt login: no identity")
	}
	token, err := v.settings.JWT()
	if err != nil {
		return nil, err
	}
	role := v.settings.RoleID
	if role == "" {
		role = config.ConstCloud18IdentityClientID
	}
	secret, err := client.Logical().WriteWithContext(ctx, "auth/jwt/login", map[string]interface{}{"role": role, "jwt": token})
	if err != nil {
		return nil, fmt.Errorf("vault jwt login: %w", err)
	}
	if secret == nil || secret.Auth == nil || secret.Auth.ClientToken == "" {
		return nil, errors.New("vault jwt login: no token in the answer")
	}
	client.SetToken(secret.Auth.ClientToken)
	return client, nil
}

// Read is the value of key at path: in KV v2 the field named after the key; in KV v1 (the
// database engine's static roles) "username:password".
func (v *VaultKV) Read(ctx context.Context, client *vault.Client, path, key string) (string, error) {
	if v.settings.Mode == config.VaultConfigStoreV2 {
		secret, err := client.KVv2(v.settings.Mount).Get(ctx, path)
		if err != nil {
			return "", err
		}
		s, ok := secret.Data[key].(string)
		if !ok {
			return "", fmt.Errorf("vault %s: no field %s", path, key)
		}
		return s, nil
	}
	secret, err := client.KVv1("").Get(ctx, path)
	if err != nil {
		return "", err
	}
	user, _ := secret.Data["username"].(string)
	pass, _ := secret.Data["password"].(string)
	return user + ":" + pass, nil
}

// Patch writes the fields of data at path, a new version of the secret there (KV v2): the
// rotation and a change of a credential the primary Vault holds.
func (v *VaultKV) Patch(ctx context.Context, client *vault.Client, path string, data map[string]interface{}) error {
	_, err := client.KVv2(v.settings.Mount).Patch(ctx, path, data)
	return err
}

// Put writes data at path as the whole secret (KV v2), creating it: a copy.
func (v *VaultKV) Put(ctx context.Context, client *vault.Client, path string, data map[string]interface{}) error {
	_, err := client.KVv2(v.settings.Mount).Put(ctx, path, data)
	return err
}

// RotateRole asks the database engine to rotate a static role's password.
func (v *VaultKV) RotateRole(ctx context.Context, client *vault.Client, role string) error {
	return client.KVv1("").Put(ctx, "database/rotate-role/"+role, nil)
}

// Resolve replaces in conf.Secrets each value that is a Vault path by the value Vault holds
// there (KV v2), signing in once. A key Vault does not answer keeps its path, its error
// returned by key; the values never reach an error (F10).
func (v *VaultKV) Resolve(ctx context.Context, conf *config.Config) (map[string]error, error) {
	paths := map[string]string{}
	for k, s := range conf.Secrets.Snapshot() {
		if conf.IsPath(s.Value) {
			paths[k] = s.Value
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	client, err := v.Connect(ctx)
	if err != nil {
		return nil, err
	}
	errs := map[string]error{}
	for k, path := range paths {
		if v.settings.Mode != config.VaultConfigStoreV2 {
			continue
		}
		value, err := v.Read(ctx, client, path, k)
		if err != nil {
			errs[k] = err
			continue
		}
		if value != "" {
			conf.Secrets.Set(k, config.Secret{Value: value})
		}
	}
	return errs, nil
}

// Name is the credential's source shown in the GUI.
func (v *VaultKV) Name() string { return SourceVault }

// Versioned: KV v2 keeps the versions, a rotation can be undone.
func (v *VaultKV) Versioned() bool { return v.settings.Mode == config.VaultConfigStoreV2 }

// Generates: the database engine makes its own passwords.
func (v *VaultKV) Generates() bool { return v.settings.Mode == config.VaultDbEngine }

func (v *VaultKV) heldPath(key string) (string, bool) {
	if v.path == nil {
		return "", false
	}
	return v.path(key)
}

// Get reads key at the path its config value holds.
func (v *VaultKV) Get(key string) (string, bool, error) {
	path, ok := v.heldPath(key)
	if !ok {
		return "", false, nil
	}
	ctx := context.Background()
	client, err := v.Connect(ctx)
	if err != nil {
		return "", false, err
	}
	value, err := v.Read(ctx, client, path, key)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// CanSet: a key held at a Vault path takes a new KV v2 version; the database engine makes its
// own passwords.
func (v *VaultKV) CanSet(key string) error {
	if _, ok := v.heldPath(key); !ok {
		return fmt.Errorf("%s is not at a Vault path: %w", key, ErrNotHeld)
	}
	if v.Generates() {
		return fmt.Errorf("%s is made by the Vault database engine: Vault rotates it", key)
	}
	return nil
}

// Set writes a new version of key at its path (KV v2 Patch, as the rotation does).
func (v *VaultKV) Set(key, value string) error {
	if err := v.CanSet(key); err != nil {
		return err
	}
	path, _ := v.heldPath(key)
	ctx := context.Background()
	client, err := v.Connect(ctx)
	if err != nil {
		return err
	}
	return v.Patch(ctx, client, path, map[string]interface{}{key: value})
}

// vaultSecretPath is where a copy of key goes: <space>/<path>/<key>, path being
// <kind>/<object name>.
func vaultSecretPath(item StoreItem) string {
	return strings.Trim(item.Path, "/") + "/" + item.Key
}

// WriteAll writes the copies, signing in once; each write (and the sign-in) within timeout.
// A copy is a whole secret, one field named after the key, at <path>/<key>. It returns the
// items not written and the first error.
func (v *VaultKV) WriteAll(ctx context.Context, items []StoreItem, timeout Timeout) ([]StoreItem, error) {
	lctx, cancel := timeout.context(ctx)
	client, err := v.Connect(lctx)
	cancel()
	if err != nil {
		return items, fmt.Errorf("vault login: %w", err)
	}
	var failed []StoreItem
	var first error
	for _, it := range items {
		wctx, cancel := timeout.context(ctx)
		err := v.Put(wctx, client, vaultSecretPath(it), map[string]interface{}{it.Key: it.Value})
		cancel()
		if err != nil {
			failed = append(failed, it)
			if first == nil {
				first = fmt.Errorf("%s: %w", vaultSecretPath(it), err)
			}
		}
	}
	return failed, first
}
