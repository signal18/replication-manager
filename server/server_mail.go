package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/mailer"
)

func (repman *ReplicationManager) InitMailer() error {
	if repman.Conf.MailTo == "" || repman.Conf.MailFrom == "" || repman.Conf.MailSMTPAddr == "" {
		err := fmt.Errorf("mailer config incomplete: mail-to, mail-from, and mail-smtp-addr are required")
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error initializing mailer: %v", err)
		return err
	}

	var m *mailer.Mailer
	var err error
	m, err = mailer.NewMailer(repman.Conf.MailSMTPAddr, repman.Conf.MailFrom, repman.Conf.MailSMTPUser, repman.Conf.GetDecryptedValue("mail-smtp-password"), repman.Conf.MailSMTPTLSSkipVerify, repman.Conf.MailTimeout, repman.Conf.MailMaxPool)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error initializing mailer: %v", err)
		return err
	}

	repman.Mailer = m
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Mailer initialized")

	return nil
}

func (repman *ReplicationManager) SendClustersInitMail() {
	if repman.Conf.MailTo == "" || repman.Conf.MailFrom == "" || repman.Conf.MailSMTPAddr == "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Skipping clusters init mail: missing mail-to, mail-from, or mail-smtp-addr")
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending clusters init mail")

	if repman.Mailer == nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Mailer not found. Initializing mailer")
		if err := repman.InitMailer(); err != nil {
			return
		}
	}

	addr := repman.Conf.MonitorAddress
	if repman.Conf.Cloud18 {
		addr = repman.Conf.APIPublicURL
	}

	subj := fmt.Sprintf("Replication-Manager started on %s", addr)
	msg := "Replication-Manager started"
	clusternames := make([]string, 0)
	clustersponsors := make(map[string]string)
	for _, cl := range repman.Clusters {
		clusternames = append(clusternames, cl.Name)
		if repman.Conf.Cloud18 && cl.Conf.Cloud18 && cl.GetSponsorEmail() != "" {
			sponsor := cl.GetSponsorEmail()
			if _, ok := clustersponsors[sponsor]; !ok {
				clustersponsors[sponsor] = cl.Name
			} else {
				clustersponsors[sponsor] += "," + cl.Name
			}
		}
	}

	msg += "\n- Cluster: %s"
	msg += fmt.Sprintf("\n- Monitor: %s", addr)
	msg += fmt.Sprintf("\n- Version: %s", repman.Version)
	msg += fmt.Sprintf("\n- Timestamp: %s", time.Now().Format("2006-01-02 15:04:05"))
	msg += "\n\nBest regards,\nReplication Manager"
	err := repman.Mailer.SendEmailMessage(mailer.Email{Message: fmt.Sprintf(msg, strings.Join(clusternames, ", ")), Subject: subj, To: repman.Conf.MailTo, IsHTML: false})
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Error sending email: %v", err)
	}

	// Send email to sponsors
	if repman.Conf.Cloud18 {
		if len(clustersponsors) > 0 {
			for email, clusters := range clustersponsors {
				err = repman.Mailer.SendEmailMessage(mailer.Email{Message: fmt.Sprintf(msg, clusters), Subject: subj, To: email, IsHTML: false})
				if err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Error sending email for sponsor %s: %v", email, err)
				}
			}
		}
	}

	if err == nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Email with subject %s sent to %s", subj, repman.Conf.MailTo)
	}
}

// SendEmail sends an email based on the provided email struct
func (repman *ReplicationManager) SendMail(email mailer.Email) error {
	if repman.Mailer == nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Mailer not found. Initializing mailer")
		if err := repman.InitMailer(); err != nil {
			return err
		}
	}

	err := repman.Mailer.SendEmailMessage(email)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Error sending email: %v", err)
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Email with subject %s sent to %s", email.Subject, email.To)

	return err
}

// SendEmailMessage sends an email based on the provided parameters
func (repman *ReplicationManager) SendEmailMessage(msg, subj, to string, isHTML bool, attachments []string) error {
	if repman.Mailer == nil {
		if err := repman.InitMailer(); err != nil {
			return err
		}
	}

	err := repman.Mailer.SendEmailMessage(mailer.Email{Message: msg, Subject: subj, To: to, IsHTML: isHTML, Attachments: attachments})
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Error sending email: %v", err)
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModMailer, config.LvlInfo, "Email with subject %s sent to %s", subj, to)

	return err
}

func (repman *ReplicationManager) SendCloud18ClusterSubscriptionMail(clustername string, userform cluster.UserForm) error {
	err := repman.SendOwnerCloud18SubscriptionMail(clustername, userform)
	if err != nil {
		return fmt.Errorf("Cluster admin : %v", err)
	}

	err = repman.SendSponsorCloud18SubscriptionMail(clustername, userform)
	if err != nil {
		return fmt.Errorf("Cluster sponsor : %v", err)
	}
	return nil
}

func (repman *ReplicationManager) SendOwnerCloud18SubscriptionMail(clustername string, userform cluster.UserForm) error {
	to := repman.Conf.MailTo
	subj := fmt.Sprintf("Subscription Request for Cluster %s: %s", clustername, userform.Username)
	msg := fmt.Sprintf(`Dear Admin,

A new user has requested to register for the cluster service.

Details:
- User Email: %s
- Cluster: %s
- Monitoring Node: %s
- Registration Request Time: %s

Please review the registration request and take the necessary actions.

Best regards,
Replication Manager
`, userform.Username, clustername, repman.Conf.APIPublicURL, time.Now().Format("2006-01-02 15:04:05"))

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorCloud18SubscriptionMail(clustername string, userform cluster.UserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("Subscription Request for Cluster %s: %s", clustername, userform.Username)
	msg := fmt.Sprintf(`Dear Sponsor,

Thank you for submitting your request. We have successfully received it and are currently preparing to process it.

To proceed further, we kindly request you to make the payment to the bank account details provided below. Once the payment has been completed, please allow us time to verify it, and we will follow up with the next steps via email.

Registration Details:
- User Email: %s
- Cluster: %s
- Registration Request Time: %s

Bank Account Details:
Account Name: %s
Bank Name: %s
Account Number: %s
IFSC/Swift Code: %s
Reference: %s

Kindly ensure the payment reference matches the request/invoice ID to help us track your payment efficiently.

If you have any questions or need assistance, feel free to reply to this email.

We appreciate your cooperation and look forward to assisting you further.

Best regards,

%s
`, userform.Username, clustername, time.Now().Format("2006-01-02 15:04:05"), "", "", "", "", "", repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorActivationMail(cl *cluster.Cluster, userform cluster.UserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("Subscription Active for Cluster %s: %s", cl.Name, userform.Username)
	msg := fmt.Sprintf(`Dear Sponsor,

We’re excited to let you know that your subscription is now active!

As part of your subscription, you’ll soon receive an email containing your database credentials. 

You can use these credentials to access your cluster resources after the provisioning complete.

If you have any questions in the meantime, feel free to contact our support team by replying to this email.

Thank you for choosing Cloud18!

Best regards,

%s
`, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendPendingRejectionMail(cl *cluster.Cluster, userform cluster.UserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("Subscription Rejected for Cluster %s: %s", cl.Name, userform.Username)
	msg := fmt.Sprintf(`Dear Subscriber,

We regret to inform you that we are unable to process your subscription request for the cluster %s any further.

After further checking, the cluster you requested to subscribe to is currently unavailable or already subscribed by other subscriber. We encourage you to consider subscribing to a different cluster.

We understand that this may be disappointing news, and we apologize for any inconvenience this may cause. 

If you have any questions or require further clarification, please do not hesitate to contact our support team by replying to this email.

Thank you for your understanding.

Best regards,

%s
`, cl.Name, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorCredentialsMail(cl *cluster.Cluster) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}

	var user cluster.APIUser
	for _, u := range cl.APIUsers {
		if u.Roles[config.RoleSponsor] {
			user = u
			break
		}
	}

	if user.User == "" {
		return fmt.Errorf("No sponsor found for cluster %s", cl.Name)
	}

	to := user.User

	subj := fmt.Sprintf("DB Credentials for Cluster %s: %s", cl.Name, user.User)
	msg := fmt.Sprintf(`Dear Sponsor,

We are pleased to provide you with the necessary credentials to access your database. Please find the connection details below:

- Cloud18 DB Read-Write Split Server: %s
- Cloud18 DB Read-Write Server: %s
- Cloud18 DB Read-Only Server: %s
- Username: %s
- Password: %s

If you require assistance with connecting to the database, please do not hesitate to contact our support team.

This email contains confidential information. Please do not share it with unauthorized individuals. If you are not the intended recipient, kindly delete this email immediately and notify us.

Thank you for choosing Cloud18. We are committed to supporting your success.

Best regards,

%s
`, cl.Conf.Cloud18DatabaseReadWriteSplitSrvRecord, cl.Conf.Cloud18DatabaseReadWriteSrvRecord, cl.Conf.Cloud18DatabaseReadSrvRecord, cl.GetSponsorUser(), cl.GetSponsorPass(), repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendDBACredentialsMail(cl *cluster.Cluster, dest, delegator string) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := make([]string, 0)
	if dest == "dbops" {
		for _, u := range cl.APIUsers {
			if u.Roles[config.RoleDBOps] || u.Roles[config.RoleExtDBOps] {
				if u.User == "admin" {
					continue
				}
				to = append(to, u.User)
			}
		}

		if len(to) == 0 {
			if repman.Conf.MailTo != "" {
				to = append(to, repman.Conf.MailTo)
			} else {
				return fmt.Errorf("No valid email destination for cluster %s", cl.Name)
			}
		}
	} else {
		to = strings.Split(dest, ",")
		if len(to) == 0 {
			return fmt.Errorf("No valid email destination for cluster %s", cl.Name)
		}
	}
	subj := fmt.Sprintf("DB Credentials for Cluster %s", cl.Name)
	msg := fmt.Sprintf(`Dear DBA,

User %s has delegated you to access the database.

Please find below the credentials required to connect to the database:

- Cloud18 DB Read-Write Split Server: %s
- Cloud18 DB Read-Write Server: %s
- Cloud18 DB Read-Only Server: %s
- Username: %s
- Password: %s

Please treat this information as confidential and do not share it with unauthorized individuals. If you are not the intended recipient, please delete this email immediately and notify us.

Thank you for your attention to this matter.

Best regards,

%s
`, delegator, cl.Conf.Cloud18DatabaseReadWriteSplitSrvRecord, cl.Conf.Cloud18DatabaseReadWriteSrvRecord, cl.Conf.Cloud18DatabaseReadSrvRecord, cl.GetDbaUser(), cl.GetDbaPass(), repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, strings.Join(to, ","), false, nil)
}

func (repman *ReplicationManager) SendSysAdmCredentialsMail(cl *cluster.Cluster, dest, delegator string) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := make([]string, 0)
	if dest == "sysops" {
		for _, u := range cl.APIUsers {
			if u.Roles[config.RoleDBOps] || u.Roles[config.RoleExtDBOps] {
				if u.User == "admin" {
					continue
				}
				to = append(to, u.User)
			}
		}

		if len(to) == 0 {
			if repman.Conf.MailTo != "" {
				to = append(to, repman.Conf.MailTo)
			} else {
				return fmt.Errorf("No valid email destination for cluster %s", cl.Name)
			}
		}
	} else {
		to = strings.Split(dest, ",")
		if len(to) == 0 {
			return fmt.Errorf("No valid email destination for cluster %s", cl.Name)
		}
	}

	subj := fmt.Sprintf("DB Credentials for Cluster %s", cl.Name)
	msg := fmt.Sprintf(`Dear System Admin,

User %s has delegated you to access the database server.

Please find below the credentials required to connect to the server:

- Cloud18 DB Read-Write Split Server: %s
- Cloud18 DB Read-Write Server: %s
- Cloud18 DB Read-Only Server: %s
- Username: %s
- Password: %s

Please treat this information as confidential and do not share it with unauthorized individuals. If you are not the intended recipient, please delete this email immediately and notify us.

Thank you for your attention to this matter.

Best regards,

%s
`, delegator, cl.Conf.Cloud18DatabaseReadWriteSplitSrvRecord, cl.Conf.Cloud18DatabaseReadWriteSrvRecord, cl.Conf.Cloud18DatabaseReadSrvRecord, cl.GetDbaUser(), cl.GetDbaPass(), repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, strings.Join(to, ","), false, nil)
}

func (repman *ReplicationManager) SendSponsorUnsubscribeMail(cl *cluster.Cluster, userform cluster.UserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username
	subj := fmt.Sprintf("Subscription Deactivated for Cluster %s: %s", cl.Name, userform.Username)
	msg := fmt.Sprintf(`Dear Sponsor,

We wanted to let you know that your subscription has ended. We hope you had a great experience using our services.

If you have any questions or need further assistance, please feel free to contact our support team by replying to this email.

Thank you for being a valued customer of Cloud18. We look forward to serving you again in the future.

Best regards,

%s
`, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorExternalOpsSubscriptionMail(cl *cluster.Cluster, userform CloudUserForm, partner config.Partner) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := cl.GetSponsorEmail()

	subj := fmt.Sprintf("External Ops Request for Cluster %s: %s", cl.Name, userform.Roles)
	msg := fmt.Sprintf(`Dear Sponsor,

Thank you for submitting your request. We have successfully received it and are currently preparing to propose it to our partner.

Please wait while we process your request. We will notify you once the process is complete.

Registration Details:
- Cluster: %s
- Role: %s
- Partner: %s
- Registration Request Time: %s

If you have any questions or need assistance, feel free to reply to this email.

We appreciate your cooperation and look forward to assisting you further.

Best regards,

%s
`, cl.Name, userform.Roles, partner.Name, time.Now().Format("2006-01-02 15:04:05"), repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendExternalOpsSubscriptionMail(cl *cluster.Cluster, userform CloudUserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("External Ops Request for Cluster %s: %s", cl.Name, userform.Roles)
	msg := fmt.Sprintf(`Dear Partner,

We are pleased to inform you that a user has requested your services for managing their cluster.

Please review the details below and let us know if you are available to take on this request.

Registration Details:
- Cluster: %s
- Role: %s
- Fee: %f %s

If you have any questions or need assistance, feel free to reply to this email.

We appreciate your cooperation and look forward to hearing from you.

Best regards,

%s
`, cl.Name, userform.Roles, cl.GetExternalCost(userform.Roles), cl.Conf.Cloud18CostCurrency, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorExternalOpsActivationMail(cl *cluster.Cluster, role string, partner config.Partner) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := cl.GetSponsorEmail()

	subj := fmt.Sprintf("External Ops Active for Cluster %s: %s", cl.Name, role)
	msg := fmt.Sprintf(`Dear Sponsor,

We’re excited to let you know that your subscription for external ops is now active!

The external partner (%s) you choose will now manage your cluster.

If you have any questions in the meantime, feel free to contact our support team by replying to this email.

Thank you for choosing Cloud18!

Best regards,

%s
`, partner.Name, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendExternalOpsActivationMail(cl *cluster.Cluster, userform CloudUserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("Partnership Active for Cluster %s as %s", cl.Name, userform.Roles)
	msg := fmt.Sprintf(`Dear Partner,

We’re excited to let you know that your partnership is now active!

As part of your partnership, you’ll soon receive an email containing your database credentials. 

You can use these credentials to access sponsor cluster resources after the provisioning complete.

We kindly remind you to do your best with this project. Your dedication and effort are greatly appreciated, and we are confident that your contributions will lead to successful outcomes.

If you have any questions or need assistance, please do not hesitate to reach out to us.

Best regards,

%s
`, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorPendingRejectionExternalOpsMail(cl *cluster.Cluster, role string, partner config.Partner) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := cl.GetSponsorEmail()

	subj := fmt.Sprintf("External Ops Rejected for Cluster %s: %s", cl.Name, role)
	msg := fmt.Sprintf(`Dear Sponsor,

We regret to inform you that we are unable to process your partnership request for the cluster %s any further.

After further checking, the partner (%s) you requested to collaborate with is currently unavailable. We encourage you to consider partnering with a different available partner.
We understand that this may be disappointing news, and we apologize for any inconvenience this may cause. 

If you have any questions or require further clarification, please do not hesitate to contact our support team by replying to this email.
Thank you for your understanding.

Best regards,

%s
`, cl.Name, partner.Name, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendPartnerPendingRejectionExternalOpsMail(cl *cluster.Cluster, userform CloudUserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username

	subj := fmt.Sprintf("External Ops Rejected for Cluster %s: %s", cl.Name, userform.Roles)
	msg := fmt.Sprintf(`Dear Partner,

We regret to inform you that the request of partnership for the cluster %s is already off.

Reason: %s

We understand that this may be disappointing news, and we apologize for any inconvenience this may cause.
If you have any questions or require further clarification, please do not hesitate to contact our support team by replying to this email.
Thank you for your understanding.

Best regards,

%s
`, cl.Name, userform.Reason, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendSponsorExternalOpsEndMail(cl *cluster.Cluster, role string, partner config.Partner) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := cl.GetSponsorEmail()
	subj := fmt.Sprintf("External Ops Deactivated for Cluster %s: %s", cl.Name, role)
	msg := fmt.Sprintf(`Dear Sponsor,

We wanted to let you know that your subscription with external ops (%s) has ended. We hope you had a great experience using the services.

If you have any questions or need further assistance, please feel free to contact our support team by replying to this email.

Thank you for being a valued customer. We look forward to serving you again in the future.

Best regards,

%s
`, partner.Name, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) SendPartnerExternalOpsEndMail(cl *cluster.Cluster, userform CloudUserForm) error {
	if repman.Partner.Name == "" {
		repman.Partner.Name = "Signal 18"
	}
	to := userform.Username
	subj := fmt.Sprintf("External Ops Deactivated for Cluster %s: %s", cl.Name, userform.Roles)
	msg := fmt.Sprintf(`Dear Partner,

We wanted to let you know that your partnership for cluster (%s) has ended.

Reason : %s

If you have any questions or need further assistance, please feel free to contact our support team by replying to this email.

Thank you for your cooperation and support.

Best regards,

%s
`, cl.Name, userform.Reason, repman.Partner.Name)

	return repman.SendEmailMessage(msg, subj, to, false, nil)
}
