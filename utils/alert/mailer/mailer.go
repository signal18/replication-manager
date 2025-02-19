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

type Email struct {
	Message     string   `json:"message"`
	Subject     string   `json:"subject"`
	To          string   `json:"to"`
	IsHTML      bool     `json:"is_html"`
	Attachments []string `json:"attachments"`
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

	m.ReinitPool()

	return m, nil
}

func (m *Mailer) Close() {
	if m.Pool != nil {
		m.Pool.Close()
	}
}

func (m *Mailer) ReinitPool() error {
	var err error
	var pool *email.Pool
	if m.Pool != nil {
		m.Pool.Close()
	}

	if m.TLS != nil {
		pool, err = email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS)
	} else {
		pool, err = email.NewPool(m.Address, m.MaxConn, m.Auth)
	}
	if err != nil {
		return err
	}

	m.Pool = pool

	return nil
}

func (m *Mailer) UpdateMaxPool(maxPool int) {
	if m.Pool != nil {
		m.Pool.Close()
	}

	m.MaxConn = maxPool
	m.ReinitPool()
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

	return m.ReinitPool()
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

	return m.ReinitPool()
}

func (m *Mailer) UpdateAddress(smtpAddr string) error {
	m.Address = smtpAddr
	return m.ReinitPool()
}

func (m *Mailer) Send(e *email.Email) error {
	if m.TLS != nil {
		err := e.SendWithTLS(m.Address, m.Auth, m.TLS)
		if err != nil {
			return err
		}
	} else {
		err := e.Send(m.Address, m.Auth)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Mailer) SendEmailMessage(edata Email) error {
	e := email.NewEmail()
	e.From = m.From
	e.To = strings.Split(edata.To, ",")
	e.Subject = edata.Subject
	if edata.IsHTML {
		e.HTML = []byte(edata.Message)
	} else {
		e.Text = []byte(edata.Message)
	}

	if len(edata.Attachments) > 0 {
		for _, attachment := range edata.Attachments {
			if _, err := e.AttachFile(attachment); err != nil {
				return err
			}
		}
	}

	return m.Send(e)
}
