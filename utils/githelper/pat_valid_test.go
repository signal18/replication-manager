// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3.

package githelper

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPATValid(t *testing.T) {
	cases := []struct {
		name      string
		token     string
		status    int
		body      string
		delay     time.Duration
		wantValid bool
		wantErr   bool
	}{
		{"active", "t", 200, `{"active":true,"revoked":false}`, 0, true, false},
		{"revoked", "t", 200, `{"active":false,"revoked":true}`, 0, false, true},
		{"inactive", "t", 200, `{"active":false,"revoked":false}`, 0, false, true},
		{"unauthorized", "t", 401, ``, 0, false, true},
		{"forbidden", "t", 403, ``, 0, false, true},
		{"server error keeps the token", "t", 500, ``, 0, true, true},
		{"not found keeps the token", "t", 404, ``, 0, true, true},
		{"unreadable reply keeps the token", "t", 200, `<html>`, 0, true, true},
		{"timeout keeps the token", "t", 200, `{"active":true}`, 300 * time.Millisecond, true, true},
		{"no token", "", 200, `{"active":true}`, 0, false, true},
	}
	saveURL, saveClient := GitLabURL, patValidClient
	defer func() { GitLabURL, patValidClient = saveURL, saveClient }()
	patValidClient = &http.Client{Timeout: 100 * time.Millisecond}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("PRIVATE-TOKEN") != c.token {
					t.Errorf("PRIVATE-TOKEN header not sent")
				}
				time.Sleep(c.delay)
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			}))
			defer srv.Close()
			GitLabURL = srv.URL
			valid, err := PATValid(c.token)
			if valid != c.wantValid || (err != nil) != c.wantErr {
				t.Errorf("PATValid = %v, %v; want valid %v, error %v", valid, err, c.wantValid, c.wantErr)
			}
		})
	}
}
