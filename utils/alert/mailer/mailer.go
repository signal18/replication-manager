package mailer

import (
	"crypto/tls"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/jordan-wright/email"
)

type Mailer struct {
	Address string
	Auth    smtp.Auth
	TLS     *tls.Config
	From    string
	Pool    *email.Pool
	MaxConn int
	Timeout time.Duration
}

func NewMailer(smtpAddr, mailFrom, smtpUser, smtpPassword string, tlsSkipVerify bool, maxConn, timeout int) (*Mailer, error) {
	m := &Mailer{
		Address: smtpAddr,
		From:    mailFrom,
		MaxConn: maxConn,
		Timeout: time.Duration(timeout) * time.Second,
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

	m.Pool, _ = email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)

	return m, nil
}

func (m *Mailer) UpdateMaxPool(maxPool int) {
	if m.Pool != nil {
		m.Pool.Close()
	}

	m.MaxConn = maxPool

	pool, err := email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
	if err != nil {
		return
	}

	m.Pool = pool
}

// UpdateTimeout updates the timeout for the mailer
// if timeout is less than or equal to 0, the timeout is set to -1
// which means no timeout
func (m *Mailer) UpdateTimeout(timeout int) {
	if timeout > 0 {
		m.Timeout = time.Duration(timeout) * time.Second
	} else {
		m.Timeout = -1
	}
}

func (m *Mailer) UpdateFrom(mailFrom string) {
	m.From = mailFrom
}

func (m *Mailer) UpdateTLSConfig(tlsSkipVerify bool) error {
	if tlsSkipVerify {
		m.TLS = &tls.Config{InsecureSkipVerify: true}
	} else {
		m.TLS = nil
	}

	m.Pool.Close()
	pool, err := email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
	if err != nil {
		return err
	}

	m.Pool = pool

	return nil
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

	m.Pool.Close()
	pool, err := email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
	if err != nil {
		return err
	}

	m.Pool = pool

	return nil
}

func (m *Mailer) UpdateAddress(smtpAddr string) error {
	m.Address = smtpAddr

	m.Pool.Close()
	pool, err := email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
	if err != nil {
		return err
	}

	m.Pool = pool

	return nil
}

func (m *Mailer) Send(e *email.Email) error {
	if m.Pool == nil {
		pool, err := email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
		if err != nil {
			return err
		}

		m.Pool = pool
	}

	return m.Pool.Send(e, m.Timeout)
}

func (m *Mailer) SendEmailMessage(msg, subj, to string, isHTML bool, attachments []string) error {
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

	return m.Send(e)
}
