// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestReadLogHistory_InvalidTimeRangeIsRejected guards against a malformed
// since/until silently becoming "no bound" (which would widen a scan the
// caller thought they were narrowing) instead of failing the request.
func TestReadLogHistory_InvalidTimeRangeIsRejected(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{WorkingDir: t.TempDir(), LogFile: t.TempDir() + "/repman.log"},
	}

	for _, tt := range []struct {
		name  string
		query string
	}{
		{"bad since", "?since=not-a-date"},
		{"bad until", "?until=not-a-date"},
		{"bad since with valid until", "?since=not-a-date&until=2024-01-01T00:00:00Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x"+tt.query, nil)
			_, truncated, err := repman.readLogHistory(req, "", "")
			if !errors.Is(err, errInvalidLogHistoryRange) {
				t.Fatalf("expected errInvalidLogHistoryRange, got %v", err)
			}
			if truncated {
				t.Error("expected truncated=false on a rejected request")
			}
		})
	}
}

// TestReadLogHistory_ValidRFC3339RangeIsAccepted is the control for the test
// above: a well-formed range must not hit the validation error path (the
// underlying scan legitimately finds nothing since no log file exists yet).
func TestReadLogHistory_ValidRFC3339RangeIsAccepted(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{WorkingDir: t.TempDir(), LogFile: t.TempDir() + "/repman.log"},
	}

	req := httptest.NewRequest("GET", "/x?since=2024-01-01T00:00:00Z&until=2024-01-02T00:00:00Z", nil)
	msgs, _, err := repman.readLogHistory(req, "", "")
	if err != nil {
		t.Fatalf("expected no error for a valid RFC3339 range, got %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected no messages (no log file written in this test), got %+v", msgs)
	}
}
