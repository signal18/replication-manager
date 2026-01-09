// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

type openSVCRequestState struct {
	mu                   sync.Mutex
	objectStatusCalls    int
	createCalls          int
	lastObjectStatusPath string
	lastObjectStatusNode string
	lastCreateNode       string
	lastCreateBody       []byte
}

type openSVCRequestSnapshot struct {
	ObjectStatusCalls    int
	CreateCalls          int
	LastObjectStatusPath string
	LastObjectStatusNode string
	LastCreateNode       string
	LastCreateBody       []byte
}

func (s *openSVCRequestState) snapshot() openSVCRequestSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return openSVCRequestSnapshot{
		ObjectStatusCalls:    s.objectStatusCalls,
		CreateCalls:          s.createCalls,
		LastObjectStatusPath: s.lastObjectStatusPath,
		LastObjectStatusNode: s.lastObjectStatusNode,
		LastCreateNode:       s.lastCreateNode,
		LastCreateBody:       append([]byte(nil), s.lastCreateBody...),
	}
}

func assertCreatePayload(body []byte, expectedPath string) error {
	var req struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("unmarshal create request: %w", err)
	}
	if req.Data == nil {
		return fmt.Errorf("missing data field in create request")
	}
	if _, ok := req.Data[expectedPath]; !ok {
		return fmt.Errorf("missing data key %q in create request", expectedPath)
	}
	return nil
}

type openSVCScenario struct {
	name                string
	objectStatusReply   func(openSVCRequestSnapshot) (int, string)
	callCount           int
	expectStatusCalls   int
	expectCreateCalls   int
	expectError         bool
	expectCreatePayload bool
}

func runOpenSVCScenario(cluster *cluster.Cluster, label string, kind string, createFn func(*opensvc.Collector, string, string, string) error, scenario openSVCScenario) error {
	var err error
	state := &openSVCRequestState{}

	collector := cluster.OpenSVCConnect()

	namespace := cluster.Name
	service := "dummy"
	agent := cluster.Agents[0]
	expectedPath := namespace + "/" + kind + "/" + service

	for i := 0; i < scenario.callCount; i++ {
		err = createFn(&collector, namespace, service, agent.HostName)
		if scenario.expectError {
			if err == nil {
				return fmt.Errorf("%s: expected error, got nil", scenario.name)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("%s: unexpected error: %w", scenario.name, err)
		}
	}

	snapshot := state.snapshot()
	if snapshot.ObjectStatusCalls != scenario.expectStatusCalls {
		return fmt.Errorf("%s: expected %d object_status call(s), got %d", scenario.name, scenario.expectStatusCalls, snapshot.ObjectStatusCalls)
	}
	if snapshot.CreateCalls != scenario.expectCreateCalls {
		return fmt.Errorf("%s: expected %d create call(s), got %d", scenario.name, scenario.expectCreateCalls, snapshot.CreateCalls)
	}
	if snapshot.ObjectStatusCalls > 0 {
		if snapshot.LastObjectStatusPath != expectedPath {
			return fmt.Errorf("%s: unexpected object_status path %q", scenario.name, snapshot.LastObjectStatusPath)
		}
		if snapshot.LastObjectStatusNode != agent.HostName {
			return fmt.Errorf("%s: unexpected object_status node %q", scenario.name, snapshot.LastObjectStatusNode)
		}
	}
	if snapshot.CreateCalls > 0 {
		if snapshot.LastCreateNode != agent.HostName {
			return fmt.Errorf("%s: unexpected create node %q", scenario.name, snapshot.LastCreateNode)
		}
		if scenario.expectCreatePayload {
			if err := assertCreatePayload(snapshot.LastCreateBody, expectedPath); err != nil {
				return fmt.Errorf("%s: %w", scenario.name, err)
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "OpenSVC %s %s passed", label, scenario.name)
	return nil
}

func runOpenSVCPreserveCases(cluster *cluster.Cluster, label string, kind string, createFn func(*opensvc.Collector, string, string, string) error) bool {
	scenarios := []openSVCScenario{
		{
			name: "skip create when exists",
			objectStatusReply: func(_ openSVCRequestSnapshot) (int, string) {
				return http.StatusOK, `{"status":0}`
			},
			callCount:           1,
			expectStatusCalls:   1,
			expectCreateCalls:   0,
			expectCreatePayload: false,
		},
		{
			name: "create when missing (404)",
			objectStatusReply: func(_ openSVCRequestSnapshot) (int, string) {
				return http.StatusNotFound, `{"status":1,"error":"not found"}`
			},
			callCount:           1,
			expectStatusCalls:   1,
			expectCreateCalls:   1,
			expectCreatePayload: true,
		},
		{
			name: "create when status says not found",
			objectStatusReply: func(_ openSVCRequestSnapshot) (int, string) {
				return http.StatusOK, `{"status":1,"error":"object not found"}`
			},
			callCount:           1,
			expectStatusCalls:   1,
			expectCreateCalls:   1,
			expectCreatePayload: true,
		},
		{
			name: "create when status says not exist",
			objectStatusReply: func(_ openSVCRequestSnapshot) (int, string) {
				return http.StatusOK, `{"status":1,"error":"object does not exist"}`
			},
			callCount:           1,
			expectStatusCalls:   1,
			expectCreateCalls:   1,
			expectCreatePayload: true,
		},
		{
			name: "second provision does not recreate",
			objectStatusReply: func(snapshot openSVCRequestSnapshot) (int, string) {
				if snapshot.CreateCalls == 0 {
					return http.StatusNotFound, `{"status":1,"error":"not found"}`
				}
				return http.StatusOK, `{"status":0}`
			},
			callCount:           2,
			expectStatusCalls:   2,
			expectCreateCalls:   1,
			expectCreatePayload: true,
		},
		{
			name: "object_status error",
			objectStatusReply: func(_ openSVCRequestSnapshot) (int, string) {
				return http.StatusInternalServerError, `{"status":1,"error":"boom"}`
			},
			callCount:           1,
			expectStatusCalls:   1,
			expectCreateCalls:   0,
			expectError:         true,
			expectCreatePayload: false,
		},
	}

	for _, scenario := range scenarios {
		if err := runOpenSVCScenario(cluster, label, kind, createFn, scenario); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "OpenSVC %s %s failed: %s", label, scenario.name, err)
			return false
		}
	}
	return true
}

func (regtest *RegTest) TestOpenSVCSecretPreserve(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runOpenSVCPreserveCases(cluster, "secret", "sec", func(c *opensvc.Collector, namespace string, service string, agent string) error {
		return c.CreateSecretV2(namespace, service, agent)
	})
}

func (regtest *RegTest) TestOpenSVCConfigPreserve(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runOpenSVCPreserveCases(cluster, "config", "cfg", func(c *opensvc.Collector, namespace string, service string, agent string) error {
		return c.CreateConfigV2(namespace, service, agent)
	})
}
