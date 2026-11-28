// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package alertmanager posts alerts to the Prometheus Alertmanager v2 API
// (POST /api/v2/alerts) with net/http and JSON only. Alertmanager expects a firing alert
// to be posted again before its resolve_timeout and a resolved one to carry endsAt: the
// caller (the cluster's alertmanager channel) keeps the firing set and resends it.
package alertmanager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Alert is one alert of the v2 API (postableAlert).
type Alert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	StartsAt     string            `json:"startsAt,omitempty"`
	EndsAt       string            `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
}

// Timestamp is the v2 API's time format.
func Timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// Client posts to one or more Alertmanager instances: an Alertmanager cluster expects its
// clients to post to every peer.
type Client struct {
	URLs        []string
	User        string
	Password    string
	BearerToken string
	HTTP        *http.Client
}

// New builds a client. urls is a comma-separated list of base URLs (http://am:9093); the
// path /api/v2/alerts is added when absent. user/password set basic auth, bearerToken an
// Authorization: Bearer header (the token wins when both are set).
func New(urls, user, password, bearerToken string, skipVerify bool, timeout time.Duration) *Client {
	c := &Client{User: user, Password: password, BearerToken: bearerToken}
	for _, u := range strings.Split(urls, ",") {
		if u = strings.TrimSpace(u); u != "" {
			c.URLs = append(c.URLs, endpoint(u))
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if skipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // alert-alertmanager-tls-skip-verify, the operator's choice
	}
	c.HTTP = &http.Client{Transport: tr, Timeout: timeout}
	return c
}

func endpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/api/v2/alerts") {
		return base
	}
	return base + "/api/v2/alerts"
}

// Post sends the alerts to every URL. It fails when no URL accepted them; the error names
// each URL that failed, with the credentials removed.
func (c *Client) Post(ctx context.Context, alerts []Alert) error {
	if len(c.URLs) == 0 {
		return errors.New("alert-alertmanager-url is empty")
	}
	if len(alerts) == 0 {
		return nil
	}
	body, err := json.Marshal(alerts)
	if err != nil {
		return err
	}
	var errs []error
	ok := 0
	for _, u := range c.URLs {
		if err := c.post(ctx, u, body); err != nil {
			errs = append(errs, err)
			continue
		}
		ok++
	}
	if ok == 0 {
		return c.redact(errors.Join(errs...))
	}
	return nil
}

func (c *Client) post(ctx context.Context, u string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	switch {
	case c.BearerToken != "":
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	case c.User != "" || c.Password != "":
		req.SetBasicAuth(c.User, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d: %s", redactedURL(u), resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// redactedURL is u with the password of its user info replaced (http://user:xxxxx@host).
func redactedURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "<alert-alertmanager-url>"
	}
	return p.Redacted()
}

// redact removes the password and the token from an error's text (F10): a server or a
// proxy may echo a header back.
func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range []struct{ secret, mask string }{
		{c.Password, "<alert-alertmanager-password>"},
		{c.BearerToken, "<alert-alertmanager-bearer-token>"},
	} {
		if s.secret != "" {
			msg = strings.ReplaceAll(msg, s.secret, s.mask)
		}
	}
	return errors.New(msg)
}
