package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/jordan-wright/email"
)

type TLSConfig struct {
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	ServerName         string `json:"server_name"`
}

func (t TLSConfig) ToTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: t.InsecureSkipVerify,
		ServerName:         t.ServerName,
	}
}

// Write all to json for debugging
type Mailer struct {
	Address       string      `json:"address"`
	Username      string      `json:"username"`
	Password      string      `json:"-"`
	Auth          smtp.Auth   `json:"-"`
	TLS           *TLSConfig  `json:"-"`
	From          string      `json:"from"`
	TLSSkipVerify bool        `json:"tls_skip_verify"`
	UsePool       bool        `json:"use_pool"`
	Pool          *email.Pool `json:"pool"`
	MaxConn       int         `json:"max_conn"`
	Timeout       int         `json:"timeout"`
}

type Email struct {
	Message     string   `json:"message"`
	Subject     string   `json:"subject"`
	To          string   `json:"to"`
	IsHTML      bool     `json:"is_html"`
	Attachments []string `json:"attachments"`
}

func NewMailer(smtpAddr, mailFrom, smtpUser, smtpPassword string, tlsSkipVerify bool, timeout int, maxPool int) (*Mailer, error) {
	m := &Mailer{
		Username:      smtpUser,
		Password:      smtpPassword,
		Address:       smtpAddr,
		From:          mailFrom,
		Timeout:       timeout,
		TLSSkipVerify: tlsSkipVerify,
	}

	host, _, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		return nil, err
	}

	if smtpUser != "" {
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	}

	m.TLS = &TLSConfig{ServerName: host, InsecureSkipVerify: tlsSkipVerify}
	useTLS, useStartTLS, err := CheckSMTP(m.Address, 10*time.Second, tlsSkipVerify)
	if useTLS || useStartTLS || err != nil {
		if maxPool > 0 {
			m.MaxConn = maxPool
			m.UsePool = true

			m.ReinitPool()
		}
	} else {
		m.TLS = nil
	}

	return m, nil
}

func (m *Mailer) Close() {
	if m.Pool != nil {
		m.Pool.Close()
		m.Pool = nil
	}
}

func (m *Mailer) ReinitPool() error {
	var err error
	var pool *email.Pool
	if m.Pool != nil {
		m.Close()
	}

	if !m.UsePool {
		return nil
	}

	useTLS, useStartTLS, err := CheckSMTP(m.Address, time.Duration(m.Timeout)*time.Second, m.TLSSkipVerify)
	if useTLS || useStartTLS {
		pool, err = email.NewPool(m.Address, m.MaxConn, m.Auth, m.TLS.ToTLSConfig())
		if err == nil {
			m.Pool = pool
			return nil
		}
	}

	m.UsePool = false
	if err != nil {
		return err
	}

	return nil
}

func (m *Mailer) UpdateMaxPool(maxPool int) {
	if m.Pool != nil {
		m.Close()
	}

	m.MaxConn = maxPool

	if maxPool > 0 {
		m.UsePool = true
		m.ReinitPool()
	} else {
		m.UsePool = false
	}
}

// UpdateTimeout updates the timeout for the mailer
// if timeout is less than or equal to 0, the timeout is set to -1
// which means no timeout
func (m *Mailer) UpdateTimeout(timeout int) {
	if m.Pool != nil {
		m.Close()
	}

	if timeout > 0 {
		m.Timeout = timeout
	} else {
		m.Timeout = -1
	}

	if m.UsePool {
		m.ReinitPool()
	}
}

func (m *Mailer) SetFrom(mailFrom string) {
	m.From = mailFrom
}

func (m *Mailer) UpdateTLSConfig(tlsSkipVerify bool) error {
	m.TLSSkipVerify = tlsSkipVerify

	host, _, err := net.SplitHostPort(m.Address)
	if err != nil {
		return err
	}

	if m.Pool != nil {
		m.Close()
	}

	m.TLS = &TLSConfig{ServerName: host, InsecureSkipVerify: tlsSkipVerify}
	useTLS, useStartTLS, err := CheckSMTP(m.Address, 10*time.Second, tlsSkipVerify)
	if useTLS || useStartTLS || err != nil {
		if m.UsePool {
			m.ReinitPool()
		}
	} else {
		m.TLS = nil
	}

	return nil
}

func (m *Mailer) UpdateAuth(smtpUser, smtpPassword string) error {
	if m.Pool != nil {
		m.Close()
	}

	if smtpUser != "" {
		host, _, err := net.SplitHostPort(m.Address)
		if err != nil {
			return err
		}
		m.Auth = smtp.PlainAuth("", smtpUser, smtpPassword, host)
	} else {
		m.Auth = nil
	}

	if m.UsePool {
		m.ReinitPool()
	}

	return nil
}

func (m *Mailer) UpdateAddress(smtpAddr string) error {
	if m.Pool != nil {
		m.Close()
	}

	m.Address = smtpAddr
	host, _, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		return err
	}

	if m.TLS != nil {
		m.TLS.ServerName = host
	}

	if m.UsePool {
		m.ReinitPool()
	}

	return nil
}

func (m *Mailer) Send(e *email.Email) error {
	if e.From == "" {
		return fmt.Errorf("from address not set")
	}

	if len(e.To) == 0 {
		return fmt.Errorf("to address not set")
	}

	if m.UsePool && m.Pool != nil {
		err := m.Pool.Send(e, time.Duration(m.Timeout)*time.Second)
		if err != nil {
			m.UsePool = false
			return err
		}
	}

	useTLS, useStartTLS, err := CheckSMTP(m.Address, time.Duration(m.Timeout)*time.Second, m.TLSSkipVerify)
	if err != nil {
		return err
	} else {
		if useTLS {
			err := e.SendWithTLS(m.Address, m.Auth, m.TLS.ToTLSConfig())
			if err != nil {
				return err
			}
		} else if useStartTLS {
			err := e.SendWithStartTLS(m.Address, m.Auth, m.TLS.ToTLSConfig())
			if err != nil {
				return err
			}
		} else {
			err := e.Send(m.Address, m.Auth)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Mailer) SendEmailMessage(edata Email) error {
	if m.From == "" {
		return fmt.Errorf("from address not set")
	}

	if edata.To == "" {
		return fmt.Errorf("to address not set")
	}

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

// CheckSMTP checks if the SMTP server supports TLS or STARTTLS
func CheckSMTP(address string, timeout time.Duration, skipVerify bool) (supportsTLS bool, supportsSTARTTLS bool, err error) {
	// Split address into server and port
	server, port, err := net.SplitHostPort(address)
	if err != nil {
		return false, false, fmt.Errorf("failed to split address: %v", err)
	}

	// Set timeout for connection
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false, false, fmt.Errorf("connection error: %v", err)
	}
	defer conn.Close()

	// Check for direct TLS connection (SMTPS on port 465)
	if port == "465" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: server, InsecureSkipVerify: skipVerify})
		err = tlsConn.Handshake()
		if err != nil {
			return false, false, fmt.Errorf("TLS handshake failed: %v", err)
		}
		return true, false, nil // Implicit TLS, no STARTTLS
	}

	// SMTP Client
	client, err := smtp.NewClient(conn, server)
	if err != nil {
		return false, false, fmt.Errorf("failed to create SMTP client: %v", err)
	}
	defer client.Close()

	// Check STARTTLS support
	if ok, _ := client.Extension("STARTTLS"); ok {
		return false, true, nil
	}

	return false, false, nil
}
