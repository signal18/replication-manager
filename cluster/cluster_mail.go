package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/mailer"
)

func (cluster *Cluster) InitMailer() error {
	var m *mailer.Mailer
	var err error
	m, err = mailer.NewMailer(cluster.Conf.MailSMTPAddr, cluster.Conf.MailFrom, cluster.Conf.MailSMTPUser, cluster.Conf.GetDecryptedValue("mail-smtp-password"), cluster.Conf.MailSMTPTLSSkipVerify, cluster.Conf.MailTimeout, cluster.Conf.MailMaxPool)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error initializing mailer: %v", err)
		return err
	}

	cluster.Mailer = m
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster mailer initialized successfully")

	return nil
}

type AlertRecipient struct {
	To        string
	DbOps     bool
	SysOps    bool
	ExtDbOps  bool
	ExtSysOps bool
	Sponsor   bool
	All       bool
}

func (cluster *Cluster) GetAlertRecipients(recipient AlertRecipient) string {
	list := make(map[string]struct{})

	// Add initial recipients
	for _, email := range strings.Split(recipient.To, ",") {
		email = strings.TrimSpace(email)
		if email != "" {
			list[email] = struct{}{}
		}
	}

	if cluster.Conf.Cloud18 {
		if recipient.All || (recipient.SysOps && cluster.Conf.Cloud18GitUser != "") {
			list[cluster.Conf.Cloud18GitUser] = struct{}{}
		}
		if recipient.All || (recipient.ExtSysOps && cluster.Conf.Cloud18ExternalSysOps != "" && cluster.Conf.Cloud18ExternalSysOps != cluster.Conf.Cloud18GitUser) {
			list[cluster.Conf.Cloud18ExternalSysOps] = struct{}{}
		}
		if recipient.All || (recipient.DbOps && cluster.Conf.Cloud18DbOps != "" && cluster.Conf.Cloud18DbOps != cluster.Conf.Cloud18GitUser) {
			list[cluster.Conf.Cloud18DbOps] = struct{}{}
		}
		if recipient.All || (recipient.ExtDbOps && cluster.Conf.Cloud18ExternalDbOps != "" && cluster.Conf.Cloud18ExternalDbOps != cluster.Conf.Cloud18GitUser) {
			list[cluster.Conf.Cloud18ExternalDbOps] = struct{}{}
		}
		if recipient.All || recipient.Sponsor {
			email := cluster.GetSponsorEmail()
			if email != "" {
				list[email] = struct{}{}
			}
		}
	}

	// Build final recipient list
	var to []string
	for email := range list {
		to = append(to, email)
	}

	return strings.Join(to, ",")
}

func (cluster *Cluster) GetInstanceAddress() string {
	address := cluster.Conf.MonitorAddress
	if cluster.Conf.Cloud18 && cluster.Conf.APIPublicURL != "" {
		address = cluster.Conf.APIPublicURL
	}

	return address
}

func (cluster *Cluster) ToAlertMessage(msg string) string {
	return fmt.Sprintf("Alert: %s\nMonitor: %s\nCluster: %s\n", msg, cluster.GetInstanceAddress(), cluster.Name)
}

func (cluster *Cluster) SendMail(em mailer.Email) error {
	if cluster.Mailer == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Mailer not initialized. Initializing...")
		if err := cluster.InitMailer(); err != nil {
			return err
		}
	}

	if !strings.HasPrefix(em.Subject, "Cluster: "+cluster.Name) {
		em.Subject = "Cluster: " + cluster.Name + " - " + em.Subject
	}

	err := cluster.Mailer.SendEmailMessage(em)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Error sending email with subject %s. Err: %v", em.Subject, err)
		return err
	}

	return nil
}

// SendMail sends an email
// if the cloud18 flag is set to true
//   - if sendDbOps is true and the external dbops is set, the mail will be sent to the external dbops
//   - if sendSysOps is true and the external sysops is set, the mail will be sent to the external sysops
//
// if isAlert is true, the message will be prepended with "Alert: "
func (cluster *Cluster) SendEMailMessage(msg, subj, to string) error {
	if cluster.Mailer == nil {
		if err := cluster.InitMailer(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error init mailer when sending %s. Err: %v", subj, err)
			return err
		}
	}
	err := cluster.Mailer.SendEmailMessage(mailer.Email{Message: msg, Subject: subj, To: to})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error sending email for with subject %s. Err: %v", subj, err)
		return err
	}

	return nil
}
