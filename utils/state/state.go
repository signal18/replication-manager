// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package state

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

type State struct {
	ErrKey    string
	ErrType   string
	ErrDesc   string
	ErrFrom   string
	ServerUrl string
}

type StateHttp struct {
	ErrNumber string `json:"number"`
	ErrDesc   string `json:"desc"`
	ErrFrom   string `json:"from"`
}

type Map map[string]State

func NewMap() *Map {
	m := make(Map)
	return &m
}

func (m Map) Add(key string, s State) {

	_, ok := m[key]
	if !ok {
		m[key] = s

	}
}

func (m Map) Delete(key string) {
	delete(m, key)
}

func (m Map) Search(key string) bool {
	_, ok := m[key]
	if ok {
		return true
	} else {
		return false
	}

}

type StateMachine struct {
	CurState               *Map
	OldState               *Map
	discovered             bool
	lasttime               int64
	Firsttime              int64
	Uptime                 int64
	UptimeFailable         int64
	UptimeSemisync         int64
	lastState              int64
	heartbeats             int64
	avgReplicationDelay    float32
	inFailover             bool
	inSchemaMonitor        bool
	SchemaMonitorStartTime int64
	SchemaMonitorEndTime   int64
	sync.Mutex
}

type Sla struct {
	Firsttime      int64 `json:"firsttime"`
	Uptime         int64 `json:"uptime"`
	UptimeFailable int64 `json:"uptimeFailable"`
	UptimeSemisync int64 `json:"uptimeSemisync"`
}

func (SM *StateMachine) GetSla() Sla {
	var mySla Sla
	mySla.Firsttime = SM.Firsttime
	mySla.Uptime = SM.Uptime
	mySla.UptimeFailable = SM.UptimeFailable
	mySla.UptimeSemisync = SM.UptimeSemisync
	return mySla
}

func (SM *StateMachine) SetSla(mySla Sla) {
	SM.Firsttime = mySla.Firsttime
	SM.Uptime = mySla.Uptime
	SM.UptimeFailable = mySla.UptimeFailable
	SM.UptimeSemisync = mySla.UptimeSemisync
}

func (SM *StateMachine) Init() {

	SM.CurState = NewMap()
	SM.OldState = NewMap()
	SM.discovered = false
	SM.lasttime = time.Now().Unix()
	SM.Firsttime = SM.lasttime
	SM.Uptime = 0
	SM.UptimeFailable = 0
	SM.UptimeSemisync = 0
	SM.lastState = 0
	SM.heartbeats = 0
}

func (SM *StateMachine) SetMonitorSchemaState() {
	SM.Lock()
	SM.SchemaMonitorStartTime = time.Now().Unix()
	SM.inSchemaMonitor = true
	SM.Unlock()
}
func (SM *StateMachine) RemoveMonitorSchemaState() {
	SM.Lock()
	SM.inSchemaMonitor = false
	SM.SchemaMonitorEndTime = time.Now().Unix()
	SM.Unlock()
}

func (SM *StateMachine) SetFailoverState() {
	SM.Lock()
	SM.inFailover = true
	SM.Unlock()
}

func (SM *StateMachine) RemoveFailoverState() {
	SM.Lock()
	SM.inFailover = false
	SM.Unlock()
}

func (SM *StateMachine) IsInFailover() bool {
	return SM.inFailover
}

func (SM *StateMachine) IsInSchemaMonitor() bool {
	return SM.inSchemaMonitor
}

func (SM *StateMachine) AddState(key string, s State) {
	s.ErrKey = key
	SM.Lock()
	SM.CurState.Add(key, s)
	if SM.heartbeats == 0 {
		SM.OldState.Add(key, s)
	}
	SM.Unlock()
}

func (SM *StateMachine) IsInState(key string) bool {
	SM.Lock()
	//log.Printf("%s,%s", key, SM.OldState.Search(key))
	//CurState may not be valid depending when it's call because empty at every ticker so may have not collected the state yet

	if SM.OldState.Search(key) == false {
		SM.Unlock()
		return false
	} else {
		SM.Unlock()
		return true
	}
}

func (SM *StateMachine) DeleteState(key string) {
	SM.Lock()
	SM.CurState.Delete(key)
	SM.Unlock()
}

func (SM *StateMachine) GetHeartbeats() int64 {
	return SM.heartbeats
}

func (SM *StateMachine) GetUptime() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.Uptime)/float64(SM.lasttime-SM.Firsttime)), 'f', 5, 64)
	//fmt.Printf("INFO : Uptime %f", float64(SM.Uptime)/float64(time.Now().Unix()- SM.Firsttime))
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}
func (SM *StateMachine) GetUptimeSemiSync() string {

	var up = strconv.FormatFloat(float64(100*float64(SM.UptimeSemisync)/float64(SM.lasttime-SM.Firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}

func (SM *StateMachine) ResetUptime() {
	SM.lasttime = time.Now().Unix()
	SM.Firsttime = SM.lasttime
	SM.Uptime = 0
	SM.UptimeFailable = 0
	SM.UptimeSemisync = 0
}

func (SM *StateMachine) GetUptimeFailable() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.UptimeFailable)/float64(SM.lasttime-SM.Firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}

func (SM *StateMachine) IsFailable() bool {

	SM.Lock()
	for _, value := range *SM.OldState {
		if value.ErrType == "ERROR" {
			SM.Unlock()
			return false
		}
	}
	SM.discovered = true
	SM.Unlock()
	return true

}

func (SM *StateMachine) SetMasterUpAndSync(IsSemiSynced bool, IsNotDelay bool) {
	var timenow int64
	timenow = time.Now().Unix()
	if IsSemiSynced == true && SM.IsFailable() == true {
		SM.UptimeSemisync = SM.UptimeSemisync + (timenow - SM.lasttime)
	}
	if IsNotDelay == true && SM.IsFailable() == true {
		SM.UptimeFailable = SM.UptimeFailable + (timenow - SM.lasttime)
	}
	if SM.IsFailable() == true {
		SM.Uptime = SM.Uptime + (timenow - SM.lasttime)
	}
	SM.lasttime = timenow
	SM.heartbeats = SM.heartbeats + 1
	//fmt.Printf("INFO : is failable %b IsSemiSynced %b  IsNotDelay %b Uptime %d UptimeFailable %d UptimeSemisync %d\n",SM.IsFailable(),IsSemiSynced ,IsNotDelay, SM.Uptime, SM.UptimeFailable ,SM.UptimeSemisync)
}

// Clear copies the current map to argument map and clears it
func (SM *StateMachine) ClearState() {
	SM.Lock()
	SM.OldState = SM.CurState
	SM.CurState = nil
	SM.CurState = NewMap()
	SM.Unlock()
}

// CanMonitor checks if the current state contains errors and allows monitoring
func (SM *StateMachine) CanMonitor() bool {
	SM.Lock()
	for _, value := range *SM.CurState {
		if value.ErrType == "ERROR" {
			SM.Unlock()
			return false
		}
	}
	SM.discovered = true
	SM.Unlock()
	return true
}

func (SM *StateMachine) UnDiscovered() {
	SM.Lock()
	SM.discovered = false
	SM.Unlock()
}

func (SM *StateMachine) IsDiscovered() bool {
	return SM.discovered
}

func (SM *StateMachine) GetStates() []string {
	var log []string
	SM.Lock()
	//every thing in  OldState that can't be found in curstate
	for key2, value2 := range *SM.OldState {
		if SM.CurState.Search(key2) == false {
			//log = append(log, fmt.Sprintf("%-5s %s HAS BEEN FIXED, %s", value2.ErrType, key2, value2.ErrDesc))
			log = append(log, fmt.Sprintf("RESOLV %s : %s", key2, value2.ErrDesc))
		}
	}

	for key, value := range *SM.CurState {
		if SM.OldState.Search(key) == false {
			//log = append(log, fmt.Sprintf("%-5s %s %s", value.ErrType, key, value.ErrDesc))
			log = append(log, fmt.Sprintf("OPENED %s : %s", key, value.ErrDesc))
		}
	}
	SM.Unlock()
	return log
}

func (SM *StateMachine) GetResolvedStates() []State {
	var log []State
	SM.Lock()
	for key, state := range *SM.OldState {
		if SM.CurState.Search(key) == false {
			log = append(log, state)
		}
	}

	SM.Unlock()
	return log
}

func (SM *StateMachine) GetOpenErrors() []StateHttp {
	var log []StateHttp
	SM.Lock()
	for key, value := range *SM.OldState {
		if value.ErrType == "ERROR" {
			var httplog StateHttp
			httplog.ErrDesc = value.ErrDesc
			httplog.ErrNumber = key
			httplog.ErrFrom = value.ErrFrom
			log = append(log, httplog)
		}
	}
	SM.Unlock()
	sort.SliceStable(log, func(i, j int) bool { return log[i].ErrNumber < log[j].ErrNumber })
	return log
}

func (SM *StateMachine) GetOpenWarnings() []StateHttp {
	var log []StateHttp
	SM.Lock()
	for key, value := range *SM.OldState {
		if value.ErrType != "ERROR" {
			var httplog StateHttp
			httplog.ErrDesc = value.ErrDesc
			httplog.ErrNumber = key
			httplog.ErrFrom = value.ErrFrom
			log = append(log, httplog)
		}
	}
	SM.Unlock()
	sort.SliceStable(log, func(i, j int) bool { return log[i].ErrNumber < log[j].ErrNumber })
	return log
}

func (SM *StateMachine) CopyOldStateFromUnknowServer(Url string) {

	for key, value := range *SM.OldState {
		if value.ServerUrl == Url {
			SM.AddState(key, value)
		}
	}

}
