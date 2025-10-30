// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"

	vault "github.com/hashicorp/vault/api"
	"github.com/jordan-wright/email"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
)

var logger = logrus.New()

func (cluster *Cluster) RotatePasswords() error {
	if !cluster.HasAllDbUp() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No password rotation because databases are down (or one of them).")
		return nil
	}
	if cluster.Conf.IsVaultUsed() {

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Start password rotation using Vault.")
		vconfig := vault.DefaultConfig()

		vconfig.Address = cluster.Conf.VaultServerAddr

		client, err := cluster.GetVaultConnection()

		if err != nil {
			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "unable to initialize AppRole auth method: %v", err)
			return err
		}

		if cluster.GetConf().VaultMode == VaultDbEngine {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Vault Database Engine mode activated")
			if cluster.GetDbUser() == cluster.GetRplUser() {

				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetDbUser(), nil)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
				}
			} else {

				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetDbUser(), nil)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
				}

				err = client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetRplUser(), nil)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetRplUser(), err)
				}
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Vault config store v2 mode activated")
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cluster replication is not all up, passwords can't be rotated! : %s", err)
					return nil
				}
			}

			new_password_db := misc.GetUUID()
			new_password_rpl := misc.GetUUID()

			new_password_proxysql := misc.GetUUID()

			new_password_shard := misc.GetUUID()

			if cluster.GetDbUser() == cluster.GetRplUser() {
				new_password_rpl = new_password_db
			}

			secretData_db := map[string]interface{}{
				"db-servers-credential": cluster.GetDbUser() + ":" + new_password_db,
			}

			secretData_rpl := map[string]interface{}{
				"replication-credential": cluster.GetRplUser() + ":" + new_password_rpl,
			}

			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().User, secretData_db)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Database Password rotation cancel, unable to write secret: %v", err)
				new_password_db = cluster.GetDbPass()
			}

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().RplUser, secretData_rpl)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication Password rotation cancel, unable to write secret: %v", err)
				new_password_rpl = cluster.GetRplPass()
			}

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() && cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) {

				secretData_proxysql := map[string]interface{}{
					"proxysql-password": new_password_proxysql,
				}
				_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.Conf.ProxysqlPassword, secretData_proxysql)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ProxySQL Password rotation cancel, unable to write secret: %v", err)
					new_password_proxysql = cluster.Conf.Secrets["proxysql-password"].Value
				}
				cluster.SetClusterProxyCredentialsFromConfig()
			}

			if cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() && cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) {

				secretData_shardproxy := map[string]interface{}{
					"shardproxy-credential": cluster.GetShardUser() + ":" + new_password_shard,
				}
				_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.Conf.MdbsProxyCredential, secretData_shardproxy)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Shard Proxy Password rotation cancel, unable to write secret: %v", err)
					new_password_shard = cluster.GetShardPass()
				}
				cluster.SetClusterProxyCredentialsFromConfig()

			}

			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Secret written successfully. New password generated: db-servers-credential %s, replication-credential %s", cluster.Conf.PrintSecret(new_password_db), cluster.Conf.PrintSecret(new_password_rpl))

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
					err = s.rejoinSlaveChangePassword(&ss)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of rejoinSlaveChangePassword during rotation password ", err)
					}
				}

			}

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() && cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) {
				for _, pri := range cluster.Proxies {
					if prx, ok := pri.(*ProxySQLProxy); ok {
						prx.RotateMonitoringPasswords(new_password_db)
						prx.RotateProxyPasswords(new_password_proxysql)
						prx.SetCredential(prx.User + ":" + new_password_proxysql)
					}

				}
			}
			if cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() && cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) {
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
			err = cluster.ProvisionRotatePasswords(new_password_db)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
			}

			if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", msg)
			}
			if cluster.Conf.MailTo != "" {
				msg := "A password rotation has been made\nCheck the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				subj := "Password Rotation Replication-Manager"
				go cluster.SendEMailMessage(cluster.ToAlertMessage(msg), subj, cluster.GetAlertRecipients(AlertRecipient{To: cluster.Conf.MailTo, All: true}))
			}

		}
	} else {
		if cluster.Conf.SecretKey != nil && cluster.GetConf().ConfRewrite {
			if cluster.IsVariableImmutable("db-servers-credential") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Database user credential is immutable, password rotation cancelled.")
				return nil
			}

			if cluster.IsVariableImmutable("replication-credential") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication user credential is immutable, password rotation cancelled.")
				return nil
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Start Password rotation")
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cluster replication is not all up, passwords can't be rotated!")
					return nil
				}
			}

			new_password_db := misc.GetUUID()
			new_password_rpl := misc.GetUUID()
			new_password_proxysql := misc.GetUUID()
			new_password_shard := misc.GetUUID()

			if cluster.GetDbUser() == cluster.GetRplUser() {
				new_password_rpl = new_password_db
			}

			var new_Secret config.Secret
			new_Secret.OldValue = cluster.Conf.Secrets["db-servers-credential"].Value
			new_Secret.Value = cluster.GetDbUser() + ":" + new_password_db
			cluster.Conf.Secrets["db-servers-credential"] = new_Secret

			new_Secret.OldValue = cluster.Conf.Secrets["replication-credential"].Value
			new_Secret.Value = cluster.GetRplUser() + ":" + new_password_rpl
			cluster.Conf.Secrets["replication-credential"] = new_Secret

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() && !cluster.IsVariableImmutable("proxysql-password") {
				new_Secret.OldValue = cluster.Conf.Secrets["proxysql-password"].Value
				new_Secret.Value = new_password_proxysql
				cluster.Conf.Secrets["proxysql-password"] = new_Secret
				cluster.SetClusterProxyCredentialsFromConfig()
			}

			if cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() && !cluster.IsVariableImmutable("shardproxy-credential") {
				var new_Secret config.Secret
				new_Secret.OldValue = cluster.Conf.Secrets["shardproxy-credential"].Value
				new_Secret.Value = cluster.GetShardUser() + ":" + new_password_shard
				cluster.Conf.Secrets["shardproxy-credential"] = new_Secret
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
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of rejoinSlaveChangePassword during rotation password ", err)
					}
				}

			}

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() {
				for _, pri := range cluster.Proxies {
					if prx, ok := pri.(*ProxySQLProxy); ok {
						prx.RotateMonitoringPasswords(new_password_db)
						prx.RotateProxyPasswords(new_password_proxysql)
						prx.SetCredential(prx.User + ":" + new_password_proxysql)
					}

				}
			}
			if cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() {
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
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
			}

			if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", msg)
			}
			if cluster.Conf.MailTo != "" {
				msg := "A password rotation has been made\nCheck the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				subj := "Password Rotation Replication-Manager"
				go cluster.SendEMailMessage(cluster.ToAlertMessage(msg), subj, cluster.GetAlertRecipients(AlertRecipient{To: cluster.Conf.MailTo, All: true}))
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Password rotation is done.")
			cluster.ConfigManager.SaveConfig(cluster, false)
		}
		return nil
		//cas sans vault
		//etre en dynamic config, sinon give up
		//appeler changePassword appele dans lapi et ajouter la modif des users/passwords en bdd
	}

	return nil
}

func (cluster *Cluster) SendVaultTokenByMail(Conf *config.Config) error {
	subj := "Replication-Manager Vault Token"
	msg := "Here's your vault token: " + Conf.Secrets["vault-token"].Value + ". This token allows you to view your passwords at the following address in complete security.\n" + Conf.VaultServerAddr

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
			err = e.SendWithTLS(Conf.MailSMTPAddr, smtp.PlainAuth("", Conf.MailSMTPUser, Conf.Secrets["mail-smtp-password"].Value, strings.Split(Conf.MailSMTPAddr, ":")[0]), &tls.Config{InsecureSkipVerify: true})
		} else {
			err = e.Send(Conf.MailSMTPAddr, smtp.PlainAuth("", Conf.MailSMTPUser, Conf.Secrets["mail-smtp-password"].Value, strings.Split(Conf.MailSMTPAddr, ":")[0]))
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
	cluster.Conf.ProvDockerRegistryCredentials = strings.Join(creds, ",")

	var new_secret config.Secret
	new_secret.Value = cluster.Conf.ProvDockerRegistryCredentials
	new_secret.OldValue = cluster.Conf.GetDecryptedValue("prov-docker-registry-credentials")
	cluster.Conf.Secrets["prov-docker-registry-credentials"] = new_secret

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
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Docker private registry credentials for %s removed", registry)
			}
			break
		}
	}

	cluster.Conf.ProvDockerRegistryCredentials = strings.Join(creds, ",")

	var new_secret config.Secret
	new_secret.Value = cluster.Conf.ProvDockerRegistryCredentials
	new_secret.OldValue = cluster.Conf.GetDecryptedValue("prov-docker-registry-credentials")
	cluster.Conf.Secrets["prov-docker-registry-credentials"] = new_secret

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Docker private registry credentials added for %s", registry)

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

func (cluster *Cluster) SecretLoginCheck(vars map[string]string, rbody io.ReadCloser) (*ServerMonitor, error, int) {
	var decodedData DecodedData
	body, err := io.ReadAll(rbody)
	if err != nil {
		return nil, fmt.Errorf("Decode reading body :%s", err.Error()), 500
	}

	err = json.Unmarshal(body, &decodedData)
	if err != nil {
		return nil, fmt.Errorf("Decode body :%s. Err: %s", string(body), err.Error()), 400
	}

	var node *ServerMonitor
	if vars["serverPort"] == "" {
		node = cluster.GetServerFromName(vars["serverName"])
	} else {
		node = cluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
	}
	if node == nil {
		return nil, fmt.Errorf("No server"), 500
	}
	// Decrypt the encrypted data
	key := crypto.GetSHA256Hash(node.Pass)
	iv := crypto.GetMD5Hash(node.Pass)

	decrypted, err := node.DecodeSecret(decodedData.Data, key, iv)
	if err != nil {
		return nil, fmt.Errorf("Error decrypting data : %s", err.Error()), 500
	}

	if decrypted != cluster.GetDbPass() {
		return nil, fmt.Errorf("Invalid secret"), 401
	}

	return node, nil, 200
}
