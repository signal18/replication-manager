// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456789:AAMARKERSECRETTOKEN_x-y"

// The message goes to every chat, by POST /bot<token>/sendMessage with a JSON body.
func TestSendToEveryChat(t *testing.T) {
	var mu sync.Mutex
	var got []sendMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bot"+testToken+"/sendMessage" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		var m sendMessage
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Error(err)
		}
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testToken, "42, -1001234", "", time.Second)
	if err := c.Send(context.Background(), c.Format("Alert", "ERR00042 on db1")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ChatID != "42" || got[1].ChatID != "-1001234" {
		t.Fatalf("chats %+v", got)
	}
	if got[0].ParseMode != "" || got[0].Text != "Alert\nERR00042 on db1" {
		t.Fatalf("plain message %+v", got[0])
	}
}

// The token is never in an error: not for a refused request, not for a server that does
// not answer (net/http quotes the URL, which holds the token).
func TestTokenRedactedFromErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	c := New(srv.URL, testToken, "42", "", time.Second)
	err := c.Send(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("a refused token must fail with the API's description: %v", err)
	}
	if strings.Contains(err.Error(), "MARKER") {
		t.Fatalf("token in the error: %v", err)
	}
	srv.Close()

	// the server is gone: the transport error quotes the URL
	err = c.Send(context.Background(), "x")
	if err == nil {
		t.Fatal("a closed server must fail")
	}
	if strings.Contains(err.Error(), "MARKER") || !strings.Contains(err.Error(), tokenMask) {
		t.Fatalf("token not redacted from the transport error: %v", err)
	}

	// a server that hangs: the timeout error quotes the URL too
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer hang.Close()
	defer close(release)
	c = New(hang.URL, testToken, "42", "", 100*time.Millisecond)
	start := time.Now()
	err = c.Send(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "MARKER") {
		t.Fatalf("timeout error %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the send timeout was not applied")
	}
}

func TestEscapeMarkdownV2(t *testing.T) {
	in := `ERR00042: db-1.example:3306 (slave) is down! a_b*c[d]e~f` + "`g`" + `>h#i+j=k|l{m}n.o\p`
	want := `ERR00042: db\-1\.example:3306 \(slave\) is down\! a\_b\*c\[d\]e\~f` + "\\`g\\`" + `\>h\#i\+j\=k\|l\{m\}n\.o\\p`
	if got := EscapeMarkdownV2(in); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	c := New("", testToken, "1", ParseModeMarkdownV2, time.Second)
	if got := c.Format("Alert: x.y", "a-b"); got != "*Alert: x\\.y*\na\\-b" {
		t.Fatalf("MarkdownV2 format %q", got)
	}
	if c.APIURL != DefaultAPIURL {
		t.Fatalf("default API URL %q", c.APIURL)
	}
	if New("", testToken, "1", "HTML", time.Second).ParseMode != "" {
		t.Fatal("an unsupported parse mode must fall back to plain text")
	}
}
