package cluster

import (
	"fmt"
	"slices"
	"strings"

	"github.com/signal18/replication-manager/config"
)

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

// SendMail sends an email
// if the cloud18 flag is set to true
//   - if sendDbOps is true and the external dbops is set, the mail will be sent to the external dbops
//   - if sendSysOps is true and the external sysops is set, the mail will be sent to the external sysops
//
// if isAlert is true, the message will be prepended with "Alert: "
func (cluster *Cluster) SendMail(msg, subj, to string) error {
	err := cluster.Mailer.SendEmailMessage(msg, subj, to, cluster.Conf.MailSMTPTLSSkipVerify, false, nil)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error sending email for with subject %s. Err: %v", subj, err)
		return err
	}

	return nil
}
