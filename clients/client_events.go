//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// cliEventsMaxErrorBytes bounds what is read of an error answer.
const cliEventsMaxErrorBytes = 64 << 10

// cliStreamEvents writes every event of a server (GET .../events, list mode)
// to out as one JSON array. It asks for one page at a time (the server's page
// size: monitoring-event-status-max-definitions) and decodes and writes the
// events one by one, so only the event being written is held, never a page
// or the whole list. It stops on an empty page or once X-Total-Count events
// were written.
func cliStreamEvents(do func(*http.Request) (*http.Response, error), token, eventsURL string, out io.Writer) error {
	if _, err := io.WriteString(out, "["); err != nil {
		return err
	}
	written := 0
	for {
		req, err := http.NewRequest(http.MethodGet, eventsURL+"?offset="+strconv.Itoa(written), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := do(req)
		if err != nil {
			return err
		}
		n, total, err := cliStreamEventsPage(resp, out, written)
		resp.Body.Close()
		if err != nil {
			return err
		}
		written += n
		if n == 0 || (total >= 0 && written >= total) {
			break
		}
	}
	_, err := io.WriteString(out, "\n]\n")
	return err
}

// cliStreamEventsPage writes the events of one answer, each preceded by a
// separator when events were already written, and returns how many it wrote
// and X-Total-Count (-1 when absent).
func cliStreamEventsPage(resp *http.Response, out io.Writer, already int) (int, int, error) {
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, cliEventsMaxErrorBytes))
		return 0, -1, errors.New(strings.TrimSpace(string(msg)))
	}
	total := -1
	if v := resp.Header.Get("X-Total-Count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			total = n
		}
	}
	dec := json.NewDecoder(resp.Body)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return 0, total, fmt.Errorf("the events answer is not a JSON list: %v", err)
	}
	n := 0
	for dec.More() {
		var ev json.RawMessage
		if err := dec.Decode(&ev); err != nil {
			return n, total, err
		}
		sep := "\n"
		if already+n > 0 {
			sep = ",\n"
		}
		if _, err := io.WriteString(out, sep); err != nil {
			return n, total, err
		}
		if _, err := out.Write(ev); err != nil {
			return n, total, err
		}
		n++
	}
	return n, total, nil
}
