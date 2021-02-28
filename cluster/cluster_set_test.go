// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/utils/state"
)

func TestCluster_SetSugarState(t *testing.T) {

	sm := &state.StateMachine{}
	sm.Init()

	type fields struct {
		sme             *state.StateMachine
		expectedErrDesc string
		expectedType    string
	}
	type args struct {
		key  string
		from string
		url  string
		desc []interface{}
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "Add Error ERR00001",
			fields: fields{
				sme:             sm,
				expectedErrDesc: "Monitor freeze while running critical section",
				expectedType:    "ERROR",
			},
			args: args{
				key:  "ERR00001",
				from: "TEST",
				url:  "",
			},
		},
		{
			name: "Add Error ERR00080",
			fields: fields{
				sme:             sm,
				expectedErrDesc: "Connection use old TLS keys on foobar.com",
				expectedType:    "ERROR",
			},
			args: args{
				key:  "ERR00080",
				from: "TEST",
				url:  "",
				desc: []interface{}{
					"foobar.com",
				},
			},
		},
		{
			name: "Add Warning WARN0048",
			fields: fields{
				sme:             sm,
				expectedErrDesc: "No semisync settings on slave foobar.com",
				expectedType:    "WARN",
			},
			args: args{
				key:  "WARN0048",
				from: "TEST",
				url:  "",
				desc: []interface{}{
					"foobar.com",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &Cluster{
				sme: tt.fields.sme,
			}
			cluster.SetSugarState(tt.args.key, tt.args.from, tt.args.url, tt.args.desc...)

			// this one won't neccesarily report it
			if !tt.fields.sme.IsInState(tt.args.key) {
				t.Fatal("State not set")
			}

			// manually check
			if !tt.fields.sme.CurState.Search(tt.args.key) {
				t.Fatal("State not set")
			}

			found := false
			var comp state.State
			// now check the actual error message
			for _, state := range *tt.fields.sme.CurState {
				if state.ErrKey == tt.args.key {
					comp = state
					found = true
				}
			}

			if !found {
				t.Fatal("State not found")
			}

			if comp.ErrType != tt.fields.expectedType {
				t.Fatalf("State Type is wrong. \nGot: %s\nWant: %s", comp.ErrType, tt.fields.expectedType)
			}

			if comp.ErrFrom != tt.args.from {
				t.Fatalf("State FROM is wrong. \nGot: %s\nWant: %s", comp.ErrFrom, tt.args.from)
			}

			if comp.ServerUrl != tt.args.url {
				t.Fatalf("State URL is wrong. \nGot: %s\nWant: %s", comp.ServerUrl, tt.args.url)
			}

			if comp.ErrDesc != tt.fields.expectedErrDesc {
				t.Fatalf("State ErrDesc is wrong. \nGot: %s\nWant: %s", comp.ErrDesc, tt.fields.expectedErrDesc)
			}
		})
	}
}
