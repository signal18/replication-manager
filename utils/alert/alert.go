// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// mail.go

package alert

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/smtp"
	"strings"

	"github.com/jordan-wright/email"
	"github.com/signal18/replication-manager/config"
)

type Alert struct {
	From        string
	To          string
	State       string
	PrevState   string
	Origin      string
	Destination string
	User        string
	Password    string
	TlsVerify   bool
}

func (a *Alert) Email() error {
	e := email.NewEmail()
	e.From = a.From
	e.To = strings.Split(a.To, ",")
	subj := fmt.Sprintf("Replication-Manager alert - State change detected on host %s", a.Origin)
	e.Subject = subj
	text := fmt.Sprintf(`Replication Manager has detected a change of state for host %s.
New server state change from %s is %s.`, a.Origin, a.PrevState, a.State)
	e.Text = []byte(text)
	var err error
	if a.User == "" {
		if a.TlsVerify {
			err = e.SendWithTLS(a.Destination, nil, &tls.Config{InsecureSkipVerify: true})
		} else {
			err = e.Send(a.Destination, nil)
		}
	} else {
		if a.TlsVerify {
			err = e.SendWithTLS(a.Destination, smtp.PlainAuth("", a.User, a.Password, strings.Split(a.Destination, ":")[0]), &tls.Config{InsecureSkipVerify: true})
		} else {
			err = e.Send(a.Destination, smtp.PlainAuth("", a.User, a.Password, strings.Split(a.Destination, ":")[0]))
		}
	}

	return err
}

func (a *Alert) EmailMessage(msg string, subj string, Conf config.Config) error {

	e := email.NewEmail()
	e.From = Conf.MailFrom
	e.To = strings.Split(Conf.MailTo, ",")

	if msg == "" {
		e.Subject = fmt.Sprintf("Replication-Manager alert - State change detected on host %s", a.Origin)
		text := fmt.Sprintf(`Replication Manager has detected a change of state for host %s. New server state change from %s is %s.`, a.Origin, a.PrevState, a.State)
		e.Text = []byte(text)
	} else {
		e.Subject = subj
		e.Text = []byte(msg)
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
