package cluster

import (
	"fmt"
	"net/mail"
	"sort"
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

// GetAlertRecipients returns the addresses of an email, comma separated (issue #2313):
// recipient.To, then, for an alert (All), every user of the cluster holding alert-mail-receiver
// (the user name is the address, when it parses as one) and, on Cloud18, the owner and ops addresses of the config that
// are not users of the cluster (no user carries a grant for them). A role flag adds that role's
// address for a message meant for that role, whatever its grants.
func (cluster *Cluster) GetAlertRecipients(recipient AlertRecipient) string {
	list := make(map[string]struct{})
	add := func(email string) {
		if email = strings.TrimSpace(email); email != "" {
			list[email] = struct{}{}
		}
	}

	for _, email := range strings.Split(recipient.To, ",") {
		add(email)
	}

	users := cluster.GetAPIUsers()
	if recipient.All {
		for name, u := range users {
			if _, err := mail.ParseAddress(name); u.Grants[config.GrantAlertMailReceiver] && err == nil {
				add(name)
			}
		}
	}

	if cluster.Conf.Cloud18 {
		// For an alert, an address that is a user of the cluster is decided by its grant above.
		configAddress := func(email string, role bool) {
			if email == "" {
				return
			}
			if role {
				add(email)
				return
			}
			if _, isUser := users[email]; recipient.All && !isUser {
				add(email)
			}
		}
		configAddress(cluster.Conf.Cloud18GitUser, recipient.SysOps)
		configAddress(cluster.Conf.Cloud18ExternalSysOps, recipient.ExtSysOps)
		configAddress(cluster.Conf.Cloud18DbOps, recipient.DbOps)
		configAddress(cluster.Conf.Cloud18ExternalDbOps, recipient.ExtDbOps)
		if recipient.Sponsor {
			add(cluster.GetSponsorEmail())
		}
	}

	to := make([]string, 0, len(list))
	for email := range list {
		to = append(to, email)
	}
	sort.Strings(to)
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
