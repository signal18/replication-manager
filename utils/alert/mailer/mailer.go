package mailer

import (
	"crypto/tls"
	"net"
	"net/smtp"
	"strings"

	"github.com/jordan-wright/email"
)

type Mailer struct {
	Address string
	Auth    smtp.Auth
	TLS     *tls.Config
	From    string
	ErrorCh chan error
}

func NewMailer(smtpAddr, mailFrom, smtpUser, smtpPassword string, tlsSkipVerify bool) (*Mailer, error) {
	m := &Mailer{
		Address: smtpAddr,
		From:    mailFrom,
	}

	if smtpUser != "" {
		host, _, err := net.SplitHostPort(smtpAddr)
		if err != nil {
			return nil, err
		}
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	}

	if tlsSkipVerify {
		m.TLS = &tls.Config{InsecureSkipVerify: true}
	}
	return m, nil
}

func (m *Mailer) UpdateTLSConfig(tlsSkipVerify bool) {
	if tlsSkipVerify {
		m.TLS = &tls.Config{InsecureSkipVerify: true}
	} else {
		m.TLS = nil
	}
}

func (m *Mailer) UpdateAuth(smtpUser, smtpPassword string) error {
	if smtpUser != "" {
		host, _, err := net.SplitHostPort(m.Address)
		if err != nil {
			return err
		}
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	} else {
		m.Auth = nil
	}

	return nil
}

func (m *Mailer) UpdateAddress(smtpAddr string) {
	m.Address = smtpAddr
}

func (m *Mailer) Send(e *email.Email) error {
	return e.Send(m.Address, m.Auth)
}

func (m *Mailer) SendWithTLS(e *email.Email) error {
	return e.SendWithTLS(m.Address, m.Auth, m.TLS)
}

func (m *Mailer) SendEmailMessage(msg, subj, to string, useTLS bool, isHTML bool, attachments []string) error {
	e := email.NewEmail()
	e.From = m.From
	e.To = strings.Split(to, ",")
	e.Subject = subj
	if isHTML {
		e.HTML = []byte(msg)
	} else {
		e.Text = []byte(msg)
	}

	if len(attachments) > 0 {
		for _, attachment := range attachments {
			if _, err := e.AttachFile(attachment); err != nil {
				return err
			}
		}
	}

	if useTLS {
		return m.SendWithTLS(e)
	}
	return m.Send(e)
}
