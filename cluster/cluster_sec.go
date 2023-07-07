// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"crypto/tls"
	"net/smtp"
	"strings"

	vault "github.com/hashicorp/vault/api"
	"github.com/jordan-wright/email"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

var logger = logrus.New()

func (cluster *Cluster) RotatePasswords() error {
	if !cluster.HasAllDbUp() {
		cluster.LogPrintf(LvlErr, "No password rotation because databases are down (or one of them).")
		return nil
	}
	if cluster.Conf.IsVaultUsed() {

		cluster.LogPrintf(LvlInfo, "Start password rotation using Vault.")
		config := vault.DefaultConfig()

		config.Address = cluster.Conf.VaultServerAddr

		client, err := cluster.GetVaultConnection()

		if err != nil {
			//cluster.LogPrintf(LvlErr, "unable to initialize AppRole auth method: %v", err)
			return err
		}

		if cluster.GetConf().VaultMode == VaultDbEngine {
			cluster.LogPrintf(LvlInfo, "Vault Database Engine mode activated")
			if cluster.GetDbUser() == cluster.GetRplUser() {

				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetDbUser(), nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
				}
			} else {

				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetDbUser(), nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
				}

				err = client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetRplUser(), nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetRplUser(), err)
				}
			}
		} else {
			cluster.LogPrintf(LvlInfo, "Vault config store v2 mode activated")
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogPrintf(LvlErr, "Cluster replication is not all up, passwords can't be rotated! : %s", err)
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

			//cluster.LogPrintf(LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().User, secretData_db)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Database Password rotation cancel, unable to write secret: %v", err)
				new_password_db = cluster.GetDbPass()
			}

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().RplUser, secretData_rpl)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Replication Password rotation cancel, unable to write secret: %v", err)
				new_password_rpl = cluster.GetRplPass()
			}

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() && cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) {

				secretData_proxysql := map[string]interface{}{
					"proxysql-password": new_password_proxysql,
				}
				_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.Conf.ProxysqlPassword, secretData_proxysql)
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL Password rotation cancel, unable to write secret: %v", err)
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
					cluster.LogPrintf(LvlErr, "Shard Proxy Password rotation cancel, unable to write secret: %v", err)
					new_password_shard = cluster.GetShardPass()
				}
				cluster.SetClusterProxyCredentialsFromConfig()

			}

			//cluster.LogPrintf(LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))
			cluster.LogPrintf(LvlInfo, "Secret written successfully. New password generated: db-servers-credential %s, replication-credential %s", cluster.Conf.PrintSecret(new_password_db), cluster.Conf.PrintSecret(new_password_rpl))

			cluster.SetClusterMonitorCredentialsFromConfig()

			cluster.SetClusterReplicationCredentialsFromConfig()

			for _, srv := range cluster.Servers {
				srv.SetCredential(srv.URL, cluster.GetDbUser(), cluster.GetDbPass())
			}

			for _, u := range cluster.master.Users {
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
						cluster.LogPrintf(LvlErr, "Fail of rejoinSlaveChangePassword during rotation password ", err)
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
						for _, u := range prx.ShardProxy.Users {
							if u.User == prx.User {
								dbhelper.SetUserPassword(prx.ShardProxy.Conn, prx.ShardProxy.DBVersion, u.Host, u.User, new_password_shard)
							}

						}
					}
				}
			}
			err = cluster.ProvisionRotatePasswords(new_password_db)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
			}

			if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				cluster.LogPrintf("ALERT", msg)
			}
			if cluster.Conf.MailTo != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster.  Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				subj := "Password Rotation Replication-Manager"
				alert := alert.Alert{}
				go alert.EmailMessage(msg, subj, cluster.Conf)
			}

		}
	} else {
		if cluster.Conf.SecretKey != nil && cluster.GetConf().ConfRewrite {
			cluster.LogPrintf(LvlInfo, "Start Password rotation")
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogPrintf(LvlErr, "Cluster replication is not all up, passwords can't be rotated!")
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

			if cluster.GetConf().ProxysqlOn && cluster.HasAllProxyUp() {
				new_Secret.OldValue = cluster.Conf.Secrets["proxysql-password"].Value
				new_Secret.Value = new_password_proxysql
				cluster.Conf.Secrets["proxysql-password"] = new_Secret
				cluster.SetClusterProxyCredentialsFromConfig()
			}

			if cluster.GetConf().MdbsProxyOn && cluster.HasAllProxyUp() {
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

			for _, u := range cluster.master.Users {
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
						cluster.LogPrintf(LvlErr, "Fail of rejoinSlaveChangePassword during rotation password ", err)
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
						for _, u := range prx.ShardProxy.Users {
							if u.User == prx.User {
								dbhelper.SetUserPassword(prx.ShardProxy.Conn, prx.ShardProxy.DBVersion, u.Host, u.User, new_password_shard)
							}

						}
					}
				}
			}
			err := cluster.ProvisionRotatePasswords(new_password_db)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
			}

			if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				cluster.LogPrintf("ALERT", msg)
			}
			if cluster.Conf.MailTo != "" {
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster.  Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				subj := "Password Rotation Replication-Manager"
				alert := alert.Alert{}
				go alert.EmailMessage(msg, subj, cluster.Conf)
			}

			cluster.LogPrintf(LvlInfo, "Password rotation is done.")
			cluster.Save()
		}
		return nil
		//cas sans vault
		//etre en dynamic config, sinon give up
		//appeler changePassword appele dans lapi et ajouter la modif des users/passwords en bdd
	}

	return nil
}

func (cluster *Cluster) SendVaultTokenByMail(Conf config.Config) error {
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
		log.Println("ERROR", "Could not send mail alert because there is no valid email")
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
		log.Println("ERROR", "Could not send mail alert: %s ", err)
	}
	return err

}
