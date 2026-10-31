// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"slices"
	"strings"
	"time"

	"github.com/jordan-wright/email"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

var logger = logrus.New()

// CanRotatePasswords says whether a password rotation can run now, and why not (#2042).
// The API checks it before starting the rotation, so a refusal reaches the caller; the
// GUI shows it. Outside Vault the new passwords must have a durable place: a store keeping
// versions, or else the cluster config, saved and pushed encrypted (#2269). A credential set in
// a config file (immutable) is never rotated: repman does not write it.
func (cluster *Cluster) CanRotatePasswords() error {
	if !cluster.Conf.IsVaultUsed() {
		if err := cluster.canRotatePasswordsOutsideVault(); err != nil {
			return err
		}
	}
	// the conditions of the moment last: the GUI shows the lasting cause first
	if !cluster.HasAllDbUp() {
		return errors.New("databases are down (or one of them)")
	}
	// outside Vault's database engine, repman changes the password on every replica itself
	_, engineEntry := cluster.vaultEngineEntry()
	vaultEngine := engineEntry || cluster.Conf.IsVaultUsed() && cluster.Conf.VaultMode == VaultDbEngine
	if !vaultEngine && len(cluster.slaves) > 0 && !cluster.slaves.HasAllSlavesRunning() {
		return errors.New("replication is not running on every replica")
	}
	return nil
}

func (cluster *Cluster) canRotatePasswordsOutsideVault() error {
	if cluster.Conf.SecretKey == nil {
		return errors.New("no encryption key: the new passwords cannot be stored encrypted")
	}
	if !cluster.Conf.ConfRewrite {
		return errors.New("monitoring-save-config is off: the new passwords cannot be saved")
	}
	// the credential chain must take the new values (#2075): the version store heads it, with
	// a store keeping versions or the config as the store (#2269); a credential set in a
	// config file (immutable) is refused (#2227 rule 9)
	chain := cluster.CredentialChain()
	for _, key := range []string{"db-servers-credential", "replication-credential"} {
		if err := chain.CanSet(key); err != nil {
			return err
		}
	}
	return nil
}

// ErrCredentialChangeRefused is what a credential setter's refusal is (errors.Is, #2048): the
// credential chain's (#2075), its reason in credstore.RefusedError.
var ErrCredentialChangeRefused = credstore.ErrChangeRefused

// CanSetCredential says whether a credential set through the API would survive the next
// restart (#2048): a version in an active store, a key set in a config file included (#2227
// rule 9); without a store keeping versions the value would be lost and repman would restart
// on the former one.
func (cluster *Cluster) CanSetCredential(key string) error {
	// the credential chain decides (#2075): no store keeping versions, no change; in static mode
	// (monitoring-save-config off) the change would live in memory only, refused for the
	// database credentials, whose loss at the next restart locks repman out (#2048)
	chain := cluster.CredentialChain()
	if err := chain.CanSet(key); err != nil {
		return err
	}
	if !chain.Persists(key) {
		return &credstore.RefusedError{Reason: fmt.Errorf("monitoring-save-config is off, a new %s would be lost at the next restart", key)}
	}
	return nil
}

// CredentialInfo describes a database credential for the GUI: never its password (#2050).
type CredentialInfo struct {
	Key     string `json:"key"`
	User    string `json:"user"`
	Source  string `json:"source"`  // where the value comes from (credstore.Source*): versions (a version of the versioned stores), config-file (immutable), dynamic-config, runtime-config (monitoring-save-config off), default, vault
	Refusal string `json:"refusal"` // why a change through the API is refused now, empty when allowed
	// Initial is where the starting value is (#2227 rule 1): config-file, dynamic-config,
	// default; Versions how many versions the versioned stores hold, LastChange the newest's time
	Initial    string    `json:"initial"`
	Versions   int       `json:"versions"`
	LastChange time.Time `json:"lastChange,omitempty"`
}

// GetCredentialsInfo lists the monitoring and replication credentials, computed at each
// topology check.
func (cluster *Cluster) GetCredentialsInfo() []CredentialInfo {
	creds := []struct{ key, user, raw string }{
		{"db-servers-credential", cluster.GetDbUser(), cluster.Conf.User},
		{"replication-credential", cluster.GetRplUser(), cluster.Conf.RplUser},
	}
	out := make([]CredentialInfo, 0, len(creds))
	for _, c := range creds {
		info := CredentialInfo{Key: c.key, User: c.user, Source: cluster.CredentialChain().Source(c.key)}
		if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(c.raw) {
			info.Source = "vault"
		}
		if err := cluster.CanSetCredential(c.key); err != nil {
			info.Refusal = credstore.Reason(err)
		}
		info.Initial = cluster.initialSource(c.key)
		info.Versions = cluster.versionStore().Count(c.key)
		if v, ok := cluster.versionStore().Latest(c.key); ok {
			info.LastChange = v.At
		}
		out = append(out, info)
	}
	return out
}

// ApplyMonitorCredential gives every server the current monitoring credential, as the
// rotation does: the ping pool uses it at the next tick, the global pool is replaced when
// its connections are refused (#2048).
func (cluster *Cluster) ApplyMonitorCredential() {
	for _, srv := range cluster.Servers {
		srv.SetCredential(srv.URL, cluster.GetDbUser(), cluster.GetDbPass())
	}
}

// StartPasswordRotation starts the rotation in the background, refused before it starts
// when CanRotatePasswords says no, so the caller (API, MCP) gets the reason (#2042).
// RotatePasswords checks again when it runs: a condition that changed in between (a
// replica stopped) refuses it there, and only the log says so, the caller was answered.
func (cluster *Cluster) StartPasswordRotation() error {
	if err := cluster.CanRotatePasswords(); err != nil {
		return err
	}
	go cluster.RotatePasswords()
	return nil
}

// ScheduledPasswordRotation is the run of the scheduled rotation (#2269): RotatePasswords, as
// the button. A run the cluster's state refuses (a database down, replication stopped, a
// credential set in a config file) is skipped: one line in the security log, and a security
// state (WARN0274) until a scheduled rotation succeeds or the schedule is switched off.
func (cluster *Cluster) ScheduledPasswordRotation() {
	if err := cluster.RotatePasswords(); err != nil {
		reason := credstore.Reason(err)
		cluster.setScheduledRotationSkipped(reason)
		cluster.logSecurity("scheduler", "scheduled password rotation skipped: "+reason)
		return
	}
	cluster.setScheduledRotationSkipped("")
	cluster.logSecurity("scheduler", "scheduled password rotation done")
}

// setScheduledRotationSkipped keeps why the last scheduled rotation was skipped, empty when it
// ran or the schedule is off.
func (cluster *Cluster) setScheduledRotationSkipped(reason string) {
	cluster.scheduledRotationMu.Lock()
	cluster.scheduledRotationSkipped = reason
	cluster.scheduledRotationMu.Unlock()
}

// CheckScheduledPasswordRotation opens WARN0274, at each tick, while the last scheduled
// rotation was skipped (security domain, T3).
func (cluster *Cluster) CheckScheduledPasswordRotation() {
	if !cluster.Conf.SchedulerRotatePasswords || cluster.SecurityStateMachine == nil || !cluster.IsActive() {
		return
	}
	cluster.scheduledRotationMu.Lock()
	reason := cluster.scheduledRotationSkipped
	cluster.scheduledRotationMu.Unlock()
	if reason != "" {
		cluster.SecurityStateMachine.AddState("WARN0274", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0274"], reason)})
	}
}

func (cluster *Cluster) RotatePasswords() error {
	if err := cluster.CanRotatePasswords(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation refused: %s", err)
		return err
	}
	if e, ok := cluster.vaultEngineEntry(); ok {
		return cluster.rotateWithVaultEngine(e)
	}
	// the vault entries are stores of the chain (#2227): one path, the preconditions are
	// CanRotatePasswords'
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Start Password rotation")

	new_password_db := misc.GetUUID()
	new_password_rpl := misc.GetUUID()
	new_password_proxysql := misc.GetUUID()
	new_password_shard := misc.GetUUID()

	if cluster.GetDbUser() == cluster.GetRplUser() {
		new_password_rpl = new_password_db
	}

	// the new values go through the credential chain (#2075) as one unit, before any
	// server is changed: a refusal or a failure changes nothing
	chain := cluster.CredentialChain()
	rotated := []credstore.Change{
		{Key: "db-servers-credential", Value: cluster.GetDbUser() + ":" + new_password_db},
		{Key: "replication-credential", Value: cluster.GetRplUser() + ":" + new_password_rpl},
	}
	// a proxy credential the chain does not take is not rotated, its proxy left as it is
	rotateProxySQL := cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() && chain.CanSet("proxysql-password") == nil
	rotateShardProxy := cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() && chain.CanSet("shardproxy-credential") == nil
	if rotateProxySQL {
		rotated = append(rotated, credstore.Change{Key: "proxysql-password", Value: new_password_proxysql})
	}
	if rotateShardProxy {
		rotated = append(rotated, credstore.Change{Key: "shardproxy-credential", Value: cluster.GetShardUser() + ":" + new_password_shard})
	}
	if err := chain.SetAll(rotated...); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation refused, nothing changed: %s", err)
		return err
	}
	cluster.MarkSecretVersionStoreDirty()
	if rotateProxySQL || rotateShardProxy {
		cluster.SetClusterProxyCredentialsFromConfig()
	}

	cluster.SetClusterMonitorCredentialsFromConfig()

	cluster.SetClusterReplicationCredentialsFromConfig()

	for _, srv := range cluster.Servers {
		srv.SetCredential(srv.URL, cluster.GetDbUser(), cluster.GetDbPass())
	}

	for _, u := range cluster.master.Users.ToNewMap() {
		if u.User == cluster.GetDbUser() {
			dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, new_password_db)
		}
		if u.User == cluster.GetRplUser() {
			dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, new_password_rpl)
		}
	}
	for _, s := range cluster.slaves {

		for _, ss := range s.Replications {
			err := s.rejoinSlaveChangePassword(&ss)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of rejoinSlaveChangePassword during rotation password: %s", err)
			}
		}

	}

	if rotateProxySQL {
		for _, pri := range cluster.Proxies {
			if prx, ok := pri.(*ProxySQLProxy); ok {
				prx.RotateMonitoringPasswords(new_password_db)
				prx.RotateProxyPasswords(new_password_proxysql)
				prx.SetCredential(prx.User + ":" + new_password_proxysql)
			}

		}
	}
	if rotateShardProxy {
		for _, pri := range cluster.Proxies {
			if prx, ok := pri.(*MariadbShardProxy); ok {
				prx.RotateProxyPasswords(new_password_shard)
				prx.SetCredential(prx.User + ":" + new_password_shard)
				prx.ShardProxy.SetCredential(prx.ShardProxy.URL, prx.User, new_password_shard)
				for _, u := range prx.ShardProxy.Users.ToNewMap() {
					if u.User == prx.User {
						dbhelper.SetUserPassword(prx.ShardProxy.Conn, prx.ShardProxy.DBVersion, u.Host, u.User, new_password_shard)
					}

				}
			}
		}
	}
	err := cluster.ProvisionRotatePasswords(new_password_db)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password: %v", err)
	}

	if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
		msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster: the new versions are in the cluster's credential stores."
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", msg)
	}
	if cluster.Conf.MailTo != "" {
		msg := "A password rotation has been made on cluster " + cluster.Name + "\nThe new versions are in the cluster's credential stores."
		subj := "Password Rotation Replication-Manager"
		go cluster.SendEMailMessage(cluster.ToAlertMessage(msg), subj, cluster.GetAlertRecipients(AlertRecipient{To: cluster.Conf.MailTo, All: true}))
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Password rotation is done.")
	cluster.ConfigManager.SaveConfig(cluster, false)
	return nil
}

func (cluster *Cluster) SendVaultTokenByMail(Conf *config.Config) error {
	subj := "Replication-Manager Vault Token"
	msg := "Here's your vault token: " + Conf.Secrets.Get("vault-token").Value + ". This token allows you to view your passwords at the following address in complete security.\n" + Conf.VaultServerAddr

	e := email.NewEmail()
	e.From = Conf.MailFrom
	e.To = strings.Split(Conf.MailTo, ",")
	e.Subject = subj
	e.Text = []byte(msg)

	for ind, mail := range e.To {
		if strings.Contains(Conf.APIUsersExternal, mail) {
			e.To = append(e.To[:ind], e.To[(ind+1):]...)
		}
	}

	if len(e.To) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, "ERROR", "Could not send mail alert because there is no valid email")
		return nil
	}

	var err error
	if Conf.MailSMTPUser == "" {
		if Conf.MailSMTPTLSSkipVerify {
			err = e.SendWithTLS(Conf.MailSMTPAddr, nil, &tls.Config{InsecureSkipVerify: true})
		} else {
			err = e.Send(Conf.MailSMTPAddr, nil)
		}
	} else {
		if Conf.MailSMTPTLSSkipVerify {
			err = e.SendWithTLS(Conf.MailSMTPAddr, smtp.PlainAuth("", Conf.MailSMTPUser, Conf.Secrets.Get("mail-smtp-password").Value, strings.Split(Conf.MailSMTPAddr, ":")[0]), &tls.Config{InsecureSkipVerify: true})
		} else {
			err = e.Send(Conf.MailSMTPAddr, smtp.PlainAuth("", Conf.MailSMTPUser, Conf.Secrets.Get("mail-smtp-password").Value, strings.Split(Conf.MailSMTPAddr, ":")[0]))
		}
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, "ERROR", "Could not send mail for vault token alert: %s ", err)
	}
	return err

}

func (cluster *Cluster) AddDockerPrivateRegistryCredentials(registry, user, password string, update bool) error {
	// parse the registry URL and get the host and optional port
	if !strings.Contains(registry, ":") {
		registry += ":443" // default to port 443 if no port is specified
	}
	host, port, err := net.SplitHostPort(registry)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Invalid registry URL: %s", err)
		return err
	}

	creds := strings.Split(cluster.Conf.ProvDockerRegistryCredentials, ",")
	for k, cred := range creds {
		parts := strings.SplitN(cred, ":", 4)
		// Check if the registry already exists
		if parts[0] == host && parts[1] == port && parts[2] == user {
			if update {
				// Update existing credentials
				parts[3] = password
				creds[k] = strings.Join(parts, ":")
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Registry credentials for %s already exist", registry)
				return fmt.Errorf("Registry credentials for %s already exist", registry)
			}
		}
	}
	// If the registry does not exist, add new credentials
	creds = append(creds, fmt.Sprintf("%s:%s:%s:%s", host, port, user, password))
	value := strings.Join(creds, ",")
	if err := cluster.CredentialChain().Set("prov-docker-registry-credentials", value); err != nil {
		return err
	}
	cluster.Conf.ProvDockerRegistryCredentials = value

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Docker private registry credentials added for %s", registry)

	return nil
}

func (cluster *Cluster) DeleteDockerPrivateRegistryCredentials(registry, user string) error {
	// parse the registry URL and get the host and optional port
	if !strings.Contains(registry, ":") {
		registry += ":443" // default to port 443 if no port is specified
	}
	host, port, err := net.SplitHostPort(registry)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Invalid registry URL: %s", err)
		return err
	}

	creds := strings.Split(cluster.Conf.ProvDockerRegistryCredentials, ",")
	for k, cred := range creds {
		parts := strings.SplitN(cred, ":", 4)
		// Check if the registry already exists
		if parts[0] == host && parts[1] == port && parts[2] == user {
			// Remove existing credentials
			if len(creds) == 1 {
				// If this is the only credential, clear the list
				creds = []string{}
			} else {
				creds = append(creds[:k], creds[k+1:]...)
			}
			break
		}
	}

	value := strings.Join(creds, ",")
	if err := cluster.CredentialChain().Set("prov-docker-registry-credentials", value); err != nil {
		return err
	}
	cluster.Conf.ProvDockerRegistryCredentials = value

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Docker private registry credentials removed for %s", registry)

	return nil
}

func (cluster *Cluster) CreateDBUserFromConfig(role string) error {
	var err error
	switch role {
	case "dba":
		user, pass := misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
		if user == "" || pass == "" {
			if user == "" {
				user = "dba"
			}
			pass, _ = cluster.GeneratePassword()
			err = cluster.SetDatabaseCredentials(role, user+":"+pass)
			if err != nil {
				return err
			}
			user, pass = misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
		}

		err = cluster.SetDBUserCredentials(user, pass, true, "ALL PRIVILEGES ON *.*")
		if err != nil {
			return err
		}
	case "sponsor":
		user, pass := misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))

		if user == "" || pass == "" {
			if user == "" {
				user = "sponsor"
			}
			pass, _ = cluster.GeneratePassword()
			err = cluster.SetDatabaseCredentials(role, user+":"+pass)
			if err != nil {
				return err
			}

			user, pass = misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))
		}

		err = cluster.SetDBUserCredentials(user, pass, true, "ALL PRIVILEGES ON *.*")
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unknown role: %s", role)
	}

	return nil
}

func (cluster *Cluster) SetDBUserCredentials(user, pass string, withGrantOption bool, grants ...string) error {

	master := cluster.GetMaster()
	if master != nil {
		err := master.SetDBUserCredentials(user, pass, withGrantOption, grants...)
		if err != nil {
			return err
		}

		standalones := cluster.GetServersByState(stateUnconn)
		for _, server := range standalones {
			err := server.SetDBUserCredentials(user, pass, withGrantOption, grants...)
			if err != nil {
				return err
			}
		}
	} else {
		for _, server := range cluster.Servers {
			// Prevent from changing password on slave in cluster with active replication to avoid desynchronization
			// but allow it on standalone server
			if server.IsSlave && !server.IsMaster() {
				continue
			}

			err := server.SetDBUserCredentials(user, pass, withGrantOption, grants...)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (cluster *Cluster) RevokeDBUserGrants(user string) error {
	if user == "" {
		return errors.New("User and host are required")
	}

	master := cluster.GetMaster()
	if master != nil {
		for _, g := range master.Users.ToNewMap() {
			if g.User != user {
				continue
			}

			err := master.RevokeDBUserGrants(user, g.Host)
			if err != nil {
				return err
			}
		}

		standalones := cluster.GetServersByState(stateUnconn)
		for _, server := range standalones {
			for _, g := range server.Users.ToNewMap() {
				if g.User != user {
					continue
				}

				err := server.RevokeDBUserGrants(user, g.Host)
				if err != nil {
					return err
				}
			}
		}
	} else {
		for _, server := range cluster.Servers {
			// Prevent from changing password on slave in cluster with active replication to avoid desynchronization
			// but allow it on standalone server
			if server.IsSlave && !server.IsMaster() {
				continue
			}

			for _, g := range server.Users.ToNewMap() {
				if g.User != user {
					continue
				}

				err := server.RevokeDBUserGrants(user, g.Host)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type DecodedData struct {
	Data string `json:"data"`
}

// SecretLoginCheck authenticates a job-callback request and, alongside the
// resolved node, returns the full decrypted JSON payload -- not just the
// "secret" field DecodeSecret checks -- so callers that send extra fields in
// the encrypted body (e.g. handlerMuxServerJobState's "restore" field,
// server/api_database.go) can parse them without a second decrypt pass.
func (cluster *Cluster) SecretLoginCheck(vars map[string]string, rbody io.ReadCloser) (*ServerMonitor, string, int, error) {
	var decodedData DecodedData
	body, err := io.ReadAll(rbody)
	if err != nil {
		return nil, "", 500, fmt.Errorf("Error reading body: %s", err.Error())
	}

	err = json.Unmarshal(body, &decodedData)
	if err != nil {
		return nil, "", 400, fmt.Errorf("Decode body: %s. Err: %s", string(body), err.Error())
	}

	var node *ServerMonitor
	if vars["serverPort"] == "" {
		node = cluster.GetServerFromName(vars["serverName"])
	} else {
		node = cluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
	}
	if node == nil {
		return nil, "", 500, fmt.Errorf("No server")
	}
	// Decrypt the encrypted data
	key := crypto.GetSHA256Hash(node.Pass)
	iv := crypto.GetMD5Hash(node.Pass)

	secret, payload, err := node.DecodeSecret(decodedData.Data, key, iv)
	if err != nil {
		return nil, "", 500, fmt.Errorf("Error decrypting data : %s", err.Error())
	}

	if secret != cluster.GetDbPass() {
		return nil, "", 401, fmt.Errorf("Invalid secret")
	}

	return node, payload, 200, nil
}

// GetSystemAPIKey derives the `system` service account's API key from the PERSISTENT
// secret key (monitoring-key-path): HMAC-SHA256(SecretKey, "system:"+cluster), hex.
// It is the credential the stateless app/proxy compute sensor uses to log in as
// `system` via /api/login -- because apps/proxies have no DB password, they cannot use
// the DB-scoped secret-login the dbjobs use (that path validates the DB password and is
// untouched here). Derived, not stored: recomputed on demand for both validation (as
// the user's password) and injection into the jobs container, and it rotates when the
// key rotates (monitoring-secret-versioning). Empty when no persistent key is set.
func (cluster *Cluster) GetSystemAPIKey() string {
	key := cluster.Conf.SecretKey
	if len(key) == 0 {
		return ""
	}
	// Repman-WIDE identity, NOT per-cluster: /api/login is cluster-agnostic and matches
	// `system` against whichever cluster it hits first (tryLocalAuth loops repman.Clusters),
	// so a per-cluster key (…"system:"+cluster.Name) makes the sensor's key fail whenever a
	// DIFFERENT cluster's `system` user is matched first. One key across all clusters lets
	// any match authenticate; per-cluster authorization is still enforced by the URL's
	// cluster ACL (IsURLPassACL), not by the key.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("system"))
	return hex.EncodeToString(mac.Sum(nil))
}

// EnsureSystemServiceUser makes sure the `system` service account exists with the
// derived API key as its credential and the grants internal callers need (db + proxy,
// plus the resource-sensor grant for the /apu + /dbu push endpoints). It is an
// API-key-only machine account -- the app/proxy sensor logs in as `system` with the
// derived key; the dbjobs keep using secret-login (DB password), which mints the same
// system JWT regardless of this password, so that path is unaffected. An existing
// `system` (created here or lazily by secretLoginHandler) keeps its credential -- the
// password is reconciled in memory by reconcileSystemServicePassword -- and only gets the
// sensor grant added when it lacks it (#1967), with its other grants and roles kept.
// Idempotent: once the grant is there, nothing is written.
func (cluster *Cluster) EnsureSystemServiceUser() {
	apikey := cluster.GetSystemAPIKey()
	if apikey == "" {
		return // no persistent key -> cannot derive; app/proxy sensor auth stays unavailable
	}
	if u, ok := cluster.APIUsers["system"]; ok {
		// An existing `system` may predate the sensor grant (secret-login used to create
		// it with only "db proxy", #1967): add the grant, keep the others.
		if u.Grants[config.GrantClusterResourceSensor] {
			return
		}
		// The main config ACL is applied last with a strict override: a cluster entry
		// cannot change what it says, so rewriting it would repeat on every start.
		if _, ok := cluster.GetClusterUserAllowACLs(cluster.Conf.APIUsersACLAllow)["system"]; ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"`system` is defined in api-credentials-acl-allow without %s: add the grant there, the cluster ACL cannot override it", config.GrantClusterResourceSensor)
			return
		}
		allow, _ := config.GetCompactGrants(u.Grants)
		// Keep the roles of the entry; visitor is the default and comes back on reload.
		roles := strings.Fields(cluster.SaveUserRoles("system"))
		roles = slices.DeleteFunc(roles, func(r string) bool { return r == config.RoleVisitor })
		if err := cluster.UpdateUser(UserForm{
			Username: "system",
			Grants:   strings.Join(append(allow, config.GrantClusterResourceSensor), " "),
			Roles:    strings.Join(roles, " "),
		}, "admin", true); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Adding %s to the `system` service account failed: %s", config.GrantClusterResourceSensor, err)
		}
		return
	}
	cluster.AddUser(UserForm{
		Username: "system",
		Grants:   "db proxy " + config.GrantClusterResourceSensor,
		Password: apikey,
	}, "admin", true)
}

// reconcileSystemServicePassword forces the in-memory `system` credential to the CURRENT
// derived API key. It is called at the end of LoadAPIUsers so every load path (startup +
// each dynamic config reload) repairs a stale password: a `system` user persisted by an
// older binary (before AddUser honoured an explicit password) carries a random password,
// and a SecretKey change would also drift it; EnsureSystemServiceUser skips an existing
// user, so that drift would otherwise never be corrected and the compute sensor's login
// would keep failing. Auth compares this in-memory map (IsValidACL), so overwriting it
// here is the reliable fix -- no fragile per-value secret rewrite, and no recursion
// (unlike EnsureSystemServiceUser, which may AddUser -> LoadAPIUsers). Idempotent.
func (cluster *Cluster) reconcileSystemServicePassword() {
	apikey := cluster.GetSystemAPIKey()
	if apikey == "" {
		return
	}
	if u, ok := cluster.APIUsers["system"]; ok && u.Password != apikey {
		u.Password = apikey
		cluster.setAPIUser("system", u)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reconciled `system` service-account password to the current derived API key")
	}
}
