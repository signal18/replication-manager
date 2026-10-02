// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

// App database auto-create (#1870).
//
// An app asks the cluster for its own database: a template references
// {{app.db.schema}}, {{app.db.user}} and {{app.db.password}} (or sets
// app-db-auto-create = true) and the three app settings app-db-schema,
// app-db-user and app-db-pass are defaulted from the app name with a generated
// password. At app provision the schema and the user are created on the
// primary through dbhelper, every statement in the SQL log, and the user is
// granted on that schema only.
//
// Security rule (Stéphane): never override an existing user or schema. The app
// records what it created (app-db-owned); a provision that finds the user or
// the schema already present without that mark is refused, tracked as
// APPERR008 on the app, and nothing is altered. Dropping the app never drops
// the schema or the user.

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// appDbIdentifierMaxLen keeps the default schema and user inside the MySQL user
// name limit (32) which is the tightest of the two.
const appDbIdentifierMaxLen = 32

// appDbPasswordLength: generated app passwords are alphanumeric so they survive
// unquoted inside DSN URLs (mysql://, redis://) and shell commands.
const appDbPasswordLength = 24

const appDbTemplateKey = "{{app.db."

// appDbIdentifier derives a safe default identifier from an app name:
// lower-case, every character outside [a-z0-9_] becomes "_", never empty.
func appDbIdentifier(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	id := strings.Trim(b.String(), "_")
	if id == "" {
		id = "app"
	}
	if id[0] >= '0' && id[0] <= '9' {
		id = "app_" + id
	}
	if len(id) > appDbIdentifierMaxLen {
		id = id[:appDbIdentifierMaxLen]
	}
	return id
}

// generateAppDbPassword returns an alphanumeric password of appDbPasswordLength.
func generateAppDbPassword() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, appDbPasswordLength)
	max := big.NewInt(int64(len(charset)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[n.Int64()]
	}
	return string(out), nil
}

// appDbWanted: the template asks for a database when it references the app.db
// keys or sets app-db-auto-create itself.
func appDbWanted(appcnf *config.AppConfig, content []byte) bool {
	if appcnf != nil && appcnf.AppDbAutoCreate {
		return true
	}
	if bytes.Contains(content, []byte(appDbTemplateKey)) {
		return true
	}
	// tolerate "{{ app.db.x }}" spacing
	for _, line := range bytes.Split(content, []byte("\n")) {
		if i := bytes.Index(line, []byte("{{")); i >= 0 && bytes.Contains(line[i:], []byte("app.db.")) {
			return true
		}
	}
	return false
}

// ApplyAppDbDefaults fills app-db-schema, app-db-user and app-db-pass when
// auto-create is on and they are empty: schema and user from the app name,
// password generated and stored encrypted. Explicit values are kept.
func (cluster *Cluster) ApplyAppDbDefaults(appcnf *config.AppConfig) error {
	if appcnf == nil || !appcnf.AppDbAutoCreate {
		return nil
	}
	base := appDbIdentifier(appcnf.AppHost)
	if strings.TrimSpace(appcnf.AppDbSchema) == "" {
		appcnf.AppDbSchema = base
	}
	if strings.TrimSpace(appcnf.AppDbUser) == "" {
		appcnf.AppDbUser = base
	}
	if strings.TrimSpace(appcnf.AppDbPass) == "" {
		pass, err := generateAppDbPassword()
		if err != nil {
			return fmt.Errorf("app %s: generating the database password: %w", appcnf.AppHost, err)
		}
		appcnf.AppDbPass = cluster.Conf.GetEncryptedString(pass)
	}
	return nil
}

// appDbSubstitutionView is the {{app.db.*}} object handed to templates, on the
// app itself and on every sibling ({{apps.#(name=="x").db.user}}). Password is
// the stored (encrypted) value: a secret-type variable decrypts it at render
// time; a run command must read it from its environment variable instead.
type appDbSubstitutionView struct {
	Schema     string `json:"schema" groups:"apps"`
	User       string `json:"user" groups:"apps"`
	Password   string `json:"password" groups:"apps"`
	AutoCreate bool   `json:"autoCreate" groups:"apps"`
	Owned      bool   `json:"owned" groups:"apps"`
}

// appDbView returns the substitution object, or nil when the app has no
// database request so that {{app.db.*}} stays an unresolved (refused) key.
func appDbView(cnf *config.AppConfig) *appDbSubstitutionView {
	if cnf == nil || !cnf.AppDbAutoCreate {
		return nil
	}
	return &appDbSubstitutionView{Schema: cnf.AppDbSchema, User: cnf.AppDbUser, Password: cnf.AppDbPass, AutoCreate: true, Owned: cnf.AppDbOwned}
}

// appDbUserHosts is the host list the monitoring user is created for
// (SetDBCredentials): the monitor, every database node, every proxy, every app.
func (cluster *Cluster) appDbUserHosts() []string {
	seen := map[string]bool{}
	hosts := []string{}
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	add(cluster.Conf.MonitorAddress)
	for _, s := range cluster.Servers {
		if s != nil {
			add(s.Host)
		}
	}
	for _, p := range cluster.Proxies {
		if p != nil {
			add(p.GetHost())
		}
	}
	for _, a := range cluster.Apps {
		if a != nil {
			add(a.GetHost())
		}
	}
	return hosts
}

// appDbProvisionDecision is the security rule, kept pure for tests: without the
// ownership mark an existing schema or user refuses the provision.
func appDbProvisionDecision(owned, schemaExists, userExists bool, schema, user string) error {
	if owned {
		return nil
	}
	if schemaExists && userExists {
		return fmt.Errorf("schema %q and user %q already exist and are not owned by this app", schema, user)
	}
	if schemaExists {
		return fmt.Errorf("schema %q already exists and is not owned by this app", schema)
	}
	if userExists {
		return fmt.Errorf("user %q already exists and is not owned by this app", user)
	}
	return nil
}

func (app *App) setDbProvisionError(msg string) {
	app.Lock()
	app.DbProvisionError = msg
	app.Unlock()
}

// ProvisionAppDatabase creates the schema, the user and its grants for an app
// that asked for a database, on the primary, before its service is pushed.
// Idempotent on an owned database; refused on foreign objects (APPERR008).
func (cluster *Cluster) ProvisionAppDatabase(app *App) error {
	if app == nil || app.AppConfig == nil {
		return nil
	}
	cnf := app.AppConfig
	if !cnf.AppDbAutoCreate {
		app.setDbProvisionError("")
		return nil
	}
	fail := func(err error) error {
		app.setDbProvisionError(err.Error())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "App %s: database provisioning refused: %s", app.GetId(), err)
		return fmt.Errorf("app %s database: %w", app.GetId(), err)
	}
	if err := cluster.ApplyAppDbDefaults(cnf); err != nil {
		return fail(err)
	}
	schema, user := cnf.AppDbSchema, cnf.AppDbUser
	for _, id := range []string{schema, user} {
		if err := dbhelper.ValidateIdentifier(id); err != nil {
			return fail(err)
		}
	}
	master := cluster.GetMaster()
	if master == nil || master.Conn == nil {
		return fail(errors.New("no primary to create the database on"))
	}
	schemas, _, err := dbhelper.GetSchemas(master.Conn)
	if err != nil {
		return fail(fmt.Errorf("listing schemas on %s: %w", master.URL, err))
	}
	users, _, err := dbhelper.GetUsers(master.Conn, master.DBVersion)
	if err != nil {
		return fail(fmt.Errorf("listing users on %s: %w", master.URL, err))
	}
	schemaExists := false
	for _, s := range schemas {
		if s == schema {
			schemaExists = true
			break
		}
	}
	userHosts := map[string]bool{}
	for _, u := range users {
		if u != nil && u.User == user {
			userHosts[u.Host] = true
		}
	}
	if err := appDbProvisionDecision(cnf.AppDbOwned, schemaExists, len(userHosts) > 0, schema, user); err != nil {
		return fail(err)
	}

	pass := cluster.Conf.GetDecryptedPassword("app-db-pass", cnf.AppDbPass)
	conn, err := master.GetNewDBConn()
	if err != nil {
		return fail(fmt.Errorf("connecting to %s: %w", master.URL, err))
	}
	defer conn.Close()

	if !schemaExists {
		logs, err := dbhelper.CreateDatabaseIfNotExists(conn, schema)
		cluster.LogSQL(logs, err, master.URL, "App", config.LvlErr, "Create app schema: %s", err)
		if err != nil {
			return fail(fmt.Errorf("creating schema %q: %w", schema, err))
		}
		// Ours from this point: a later failure (user, grant) must not turn the
		// schema we just created into a "foreign" one that refuses the retry.
		cnf.AppDbOwned = true
		cluster.SaveAppConfigs()
	}
	hosts := cluster.appDbUserHosts()
	for _, h := range hosts {
		if userHosts[h] {
			continue // owned, already there: the password is never re-applied here (app-db-pass setter rotates it)
		}
		logs, err := dbhelper.CreateUser(conn, master.DBVersion, h, user, pass)
		cluster.LogSQL(strings.ReplaceAll(logs, pass, "*.*"), err, master.URL, "App", config.LvlErr, "Create app user: %s", err)
		if err != nil {
			return fail(fmt.Errorf("creating user %q@%q: %w", user, h, err))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ExecTimeout)*time.Second)
	defer cancel()
	connx, err := conn.Connx(ctx)
	if err != nil {
		return fail(fmt.Errorf("connecting to %s: %w", master.URL, err))
	}
	defer connx.Close()
	grant := fmt.Sprintf("ALL PRIVILEGES ON %s.*", dbhelper.QuoteMySQLIdentifier(schema))
	for _, h := range hosts {
		logs, err := dbhelper.SetUserGrants(ctx, connx, master.DBVersion, h, user, grant)
		cluster.LogSQL(logs, err, master.URL, "App", config.LvlErr, "Grant app user: %s", err)
		if err != nil {
			return fail(fmt.Errorf("granting %q@%q on %q: %w", user, h, schema, err))
		}
	}
	cnf.AppDbOwned = true
	cluster.SaveAppConfigs()
	app.setDbProvisionError("")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "App %s: database %s and user %s ready on %s (%d hosts)", app.GetId(), schema, user, master.URL, len(hosts))
	return nil
}

// RotateAppDatabasePassword applies a new app-db-pass to the user the app owns,
// on every host it was created for. Nothing happens on a database the app does
// not own.
func (cluster *Cluster) RotateAppDatabasePassword(app *App) error {
	if app == nil || app.AppConfig == nil || !app.AppConfig.AppDbAutoCreate || !app.AppConfig.AppDbOwned {
		return nil
	}
	cnf := app.AppConfig
	master := cluster.GetMaster()
	if master == nil || master.Conn == nil {
		return errors.New("no primary to rotate the app database password on")
	}
	users, _, err := dbhelper.GetUsers(master.Conn, master.DBVersion)
	if err != nil {
		return err
	}
	pass := cluster.Conf.GetDecryptedPassword("app-db-pass", cnf.AppDbPass)
	conn, err := master.GetNewDBConn()
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, u := range users {
		if u == nil || u.User != cnf.AppDbUser {
			continue
		}
		logs, err := dbhelper.SetUserPassword(conn, master.DBVersion, u.Host, u.User, pass)
		cluster.LogSQL(strings.ReplaceAll(logs, pass, "*.*"), err, master.URL, "App", config.LvlErr, "Rotate app user password: %s", err)
		if err != nil {
			return err
		}
	}
	return nil
}
