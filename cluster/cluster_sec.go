// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"strings"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

var logger = logrus.New()

func (cluster *Cluster) RotatePasswords() error {
	if cluster.IsVaultUsed() {

		cluster.LogPrintf(LvlInfo, "Vault config store v2 mode activated")
		config := vault.DefaultConfig()

		config.Address = cluster.Conf.VaultServerAddr

		client, err := vault.NewClient(config)
		if err != nil {
			log.Fatalf("unable to initialize Vault client: %v", err)
		}

		roleID := cluster.Conf.VaultRoleId
		secretID := &auth.SecretID{FromString: cluster.Conf.VaultSecretId}
		if roleID == "" || secretID == nil {
			log.Fatalf("no vault role-id or secret-id define")
		}

		appRoleAuth, err := auth.NewAppRoleAuth(
			roleID,
			secretID,
		)
		if err != nil {
			log.Fatalf("unable to initialize AppRole auth method: %v", err)
		}

		authInfo, err := client.Auth().Login(context.Background(), appRoleAuth)
		if err != nil {
			log.Fatalf("unable to initialize AppRole auth method: %v", err)
		}
		if authInfo == nil {
			log.Fatalf("unable to initialize AppRole auth method: %v", err)
		}
		if cluster.GetConf().VaultMode == VaultDbEngine {

			if cluster.GetConf().User == cluster.GetConf().RplUser {
				s := strings.Split(cluster.GetConf().User, "/")
				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+s[len(s)-1], nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", s[len(s)-1], err)
					return err
				}
			} else {
				s := strings.Split(cluster.GetConf().User, "/")
				err := client.KVv1("").Put(context.Background(), "database/rotate-role/"+s[len(s)-1], nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", s[len(s)-1], err)
					return err
				}
				s = strings.Split(cluster.GetConf().RplUser, "/")
				err = client.KVv1("").Put(context.Background(), "database/rotate-role/"+s[len(s)-1], nil)
				if err != nil {
					cluster.LogPrintf(LvlInfo, "unable to rotate passwords for %s static role: %v", s[len(s)-1], err)
					return err
				}
			}
		} else {
			if len(cluster.slaves) > 0 {
				if !cluster.slaves.HasAllSlavesRunning() {
					cluster.LogPrintf(LvlErr, "Cluster replication is not all up, passwords can't be rotated! : %s", err)
					return err
				}
			}

			new_password_db := misc.GetUUID()
			new_password_rpl := misc.GetUUID()

			if cluster.dbUser == cluster.rplUser {
				new_password_rpl = new_password_db
			}

			secretData_db := map[string]interface{}{
				"db-servers-credential": cluster.dbUser + ":" + new_password_db,
			}

			secretData_rpl := map[string]interface{}{
				"replication-credential": cluster.rplUser + ":" + new_password_rpl,
			}
			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().User, secretData_db)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Password rotation cancel, unable to write secret: %v", err)
				return err
			}

			_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), cluster.GetConf().RplUser, secretData_rpl)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Password rotation cancel, unable to write secret: %v", err)
				return err
			}
			cluster.LogPrintf(LvlInfo, "Secret written successfully. New password generated: db-servers-credential %s, replication-credential %s", new_password_db, new_password_rpl)

			for _, u := range cluster.master.Users {
				if u.User == cluster.dbUser {
					dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, new_password_db)
				}
				if u.User == cluster.rplUser {
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
			if cluster.GetConf().PushoverAppToken != "" && cluster.GetConf().PushoverUserToken != "" {
				//logger := logrus.New()
				msg := "A password rotation has been made on Replication-Manager " + cluster.Name + " cluster. Check the new password on " + cluster.Conf.VaultServerAddr + " website on path " + cluster.Conf.VaultMount + cluster.Conf.User + " and " + cluster.Conf.VaultMount + cluster.Conf.RplUser + "."
				cluster.LogPrintf(LvlErr, msg)
				//entry := logrus.NewEntry(logger)
				//msg := "COUCOU test"
				//entry.Log(logrus.ErrorLevel, msg)
				//p := pushover.NewHook(cluster.GetConf().PushoverAppToken, cluster.GetConf().PushoverUserToken)
				//p.Fire(entry)

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
