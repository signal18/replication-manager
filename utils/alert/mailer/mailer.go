package mailer

import (
	"crypto/tls"
	"log"
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
}

func NewMailer(smtpAddr, mailFrom, smtpUser, smtpPassword string, tlsSkipVerify bool) *Mailer {
	m := &Mailer{
		Address: smtpAddr,
		From:    mailFrom,
	}

	if smtpUser != "" {
		host, _, err := net.SplitHostPort(smtpAddr)
		if err != nil {
			log.Printf("ERROR: Could not parse mail host from %s: %s", smtpAddr, err)
			return m
		}
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	}

	if tlsSkipVerify {
		m.TLS = &tls.Config{InsecureSkipVerify: true}
	}
	return m
}

func (m *Mailer) UpdateTLSConfig(tlsSkipVerify bool) {
	if tlsSkipVerify {
		m.TLS = &tls.Config{InsecureSkipVerify: true}
	} else {
		m.TLS = nil
	}
}

func (m *Mailer) UpdateAuth(smtpUser, smtpPassword string) {
	if smtpUser != "" {
		host, _, err := net.SplitHostPort(m.Address)
		if err != nil {
			log.Printf("ERROR: Could not parse mail host from %s: %s", m.Address, err)
			return
		}
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	} else {
		m.Auth = nil
	}
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

func (m *Mailer) SendEmailMessage(msg, subj, to string, useTLS bool) error {
	e := email.NewEmail()
	e.From = m.From
	e.To = strings.Split(to, ",")
	e.Subject = subj
	e.Text = []byte(msg)

	if useTLS {
		return m.SendWithTLS(e)
	}
	return m.Send(e)
}
