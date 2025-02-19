package cluster

import (
	"fmt"
	"slices"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/mailer"
)

func (cluster *Cluster) InitMailer() error {
	m, err := mailer.NewMailer(cluster.Conf.MailSMTPAddr, cluster.Conf.MailFrom, cluster.Conf.MailSMTPUser, cluster.Conf.GetDecryptedValue("mail-smtp-password"), cluster.Conf.MailSMTPTLSSkipVerify, cluster.Conf.MailMaxPool, cluster.Conf.MailTimeout)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error initializing mailer: %v", err)
		return err
	}

	cluster.Mailer = m
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster mailed initialized successfully")

	return nil
}

func (cluster *Cluster) GetAlertRecipients(sendDbOps, sendSysOps bool) string {
	to := strings.Split(cluster.Conf.MailTo, ",")

	if cluster.Conf.Cloud18 {
		if cluster.Conf.Cloud18GitUser != "" && !slices.Contains(to, cluster.Conf.Cloud18GitUser) {
			to = append(to, cluster.Conf.Cloud18GitUser)
		}
		if sendDbOps && cluster.Conf.Cloud18ExternalDbOps != "" && !slices.Contains(to, cluster.Conf.Cloud18ExternalDbOps) {
			to = append(to, cluster.Conf.Cloud18ExternalDbOps)
		}
		if sendSysOps && cluster.Conf.Cloud18ExternalSysOps != "" && !slices.Contains(to, cluster.Conf.Cloud18ExternalSysOps) {
			to = append(to, cluster.Conf.Cloud18ExternalSysOps)
		}
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
		if err := cluster.InitMailer(); err != nil {
			return err
		}
	}

	if !strings.HasPrefix(em.Subject, "Cluster: "+cluster.Name) {
		em.Subject = "Cluster: " + cluster.Name + " - " + em.Subject
	}

	err := cluster.Mailer.SendEmailMessage(em)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error sending email for with subject %s. Err: %v", em.Subject, err)
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
