// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"

	vault "github.com/hashicorp/vault/api"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
)

var logger = logrus.New()

func (cluster *Cluster) RotatePasswords() error {
	if cluster.IsVaultUsed() {

		cluster.LogPrintf(LvlInfo, "Start password rotation")
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
					return err
				}
			} else {

				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetDbUser(), nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetDbUser(), err)
					return err
				}

				err = client.KVv1("").Put(context.Background(), "database/rotate-role/"+cluster.GetRplUser(), nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", cluster.GetRplUser(), err)
					return err
				}
			}
		} else {
			cluster.LogPrintf(LvlInfo, "Vault config store v2 mode activated")
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogPrintf(LvlErr, "Cluster replication is not all up, passwords can't be rotated! : %s", err)
					return err
				}
			}

			new_password_db := misc.GetUUID()
			new_password_rpl := misc.GetUUID()
			new_password_proxysql := misc.GetUUID()

			if cluster.GetDbUser() == cluster.GetRplUser() {
				new_password_rpl = new_password_db
			}

			secretData_db := map[string]interface{}{
				"db-servers-credential": cluster.GetDbUser() + ":" + new_password_db,
			}

			secretData_rpl := map[string]interface{}{
				"replication-credential": cluster.GetRplUser() + ":" + new_password_rpl,
			}

			secretData_proxysql := map[string]interface{}{
				"proxysql-password": new_password_proxysql,
			}

			cluster.LogPrintf(LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))

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

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.Conf.ProxysqlPassword, secretData_proxysql)
			if err != nil {
				cluster.LogPrintf(LvlErr, "ProxySQL Password rotation cancel, unable to write secret: %v", err)
				new_password_proxysql = cluster.GetDecryptedValue("proxysql-password")
			}
			cluster.LogPrintf(LvlErr, "TEST password Rotation new mdp : %s, %s, decrypt val %s", new_password_db, new_password_proxysql, cluster.GetDecryptedValue("proxysql-password"))
			cluster.LogPrintf(LvlInfo, "Secret written successfully. New password generated: db-servers-credential %s, replication-credential %s", new_password_db, new_password_rpl)
			var new_Secret Secret
			new_Secret.OldValue = cluster.encryptedFlags["db-servers-credential"].Value
			new_Secret.Value = cluster.GetDbUser() + ":" + new_password_db
			cluster.encryptedFlags["db-servers-credential"] = new_Secret

			new_Secret.OldValue = cluster.encryptedFlags["replication-credential"].Value
			new_Secret.Value = cluster.GetRplUser() + ":" + new_password_rpl
			cluster.encryptedFlags["replication-credential"] = new_Secret

			new_Secret.OldValue = cluster.encryptedFlags["proxysql-password"].Value
			new_Secret.Value = new_password_proxysql
			cluster.encryptedFlags["proxysql-password"] = new_Secret

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
			for _, pri := range cluster.Proxies {
				if prx, ok := pri.(*ProxySQLProxy); ok {
					prx.RotateMonitoringPasswords(new_password_db)
					prx.RotationAdminPasswords(new_password_proxysql)
					prx.SetCredential(prx.User + ":" + new_password_proxysql)
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
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + "/" + cluster.Conf.User + " and " + cluster.Conf.VaultMount + "/" + cluster.Conf.RplUser + "."
				subj := "Password Rotation Replication-Manager"
				alert := alert.Alert{}
				alert.From = cluster.Conf.MailFrom
				alert.To = cluster.Conf.MailTo
				alert.Destination = cluster.Conf.MailSMTPAddr
				alert.User = cluster.Conf.MailSMTPUser
				alert.Password = cluster.Conf.MailSMTPPassword
				alert.TlsVerify = cluster.Conf.MailSMTPTLSSkipVerify
				err := alert.EmailMessage(msg, subj)
				if err != nil {
					cluster.LogPrintf("ERROR", "Could not send mail alert: %s ", err)
				}
			}

			//ajouter le cas vaultmode = data_store_v2 -> on veut générer un nouveau mdp et aller remplacer l'ancien sur vault
			// changement des mdp dans toutes les bdd
			//si cluster not full up, give up with errors (ne pas le faire si le cluster est cassés)

		}
	} else {
		return nil
		//cas sans vault
		//etre en dynamic config, sinon give up
		//appeler changePassword appele dans lapi et ajouter la modif des users/passwords en bdd
	}

	return nil
}
