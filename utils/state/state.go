// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package state

import (
	"fmt"
	"slices"
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

type CapturedState struct {
	ErrKey     string
	ErrType    string
	ErrDesc    string
	ErrFrom    string
	ServerURLs []string
}

func (cs *CapturedState) Contains(url string) bool {
	return slices.Contains(cs.ServerURLs, url)
}

func (cs *CapturedState) Parse(s State) {
	cs.ErrKey = s.ErrKey
	cs.ErrType = s.ErrType
	cs.ErrDesc = s.ErrDesc
	cs.ErrFrom = s.ErrFrom
	cs.ServerURLs = make([]string, 0)
}

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
	CurState               *Map      `json:"-"`
	OldState               *Map      `json:"-"`
	CapturedState          *sync.Map `json:"-"`
	Discovered             bool      `json:"discovered"`
	sla                    Sla       `json:"-"`
	lastState              int64     `json:"-"`
	heartbeats             int64     `json:"-"`
	InFailover             bool      `json:"inFailover"`
	InSchemaMonitor        bool      `json:"inSchemaMonitor"`
	SchemaMonitorStartTime int64     `json:"-"`
	SchemaMonitorEndTime   int64     `json:"-"`
	sync.Mutex
}

type Sla struct {
	Firsttime      int64 `json:"firsttime"`
	Lasttime       int64 `json:"lasttime"`
	Uptime         int64 `json:"uptime"`
	UptimeFailable int64 `json:"uptimeFailable"`
	UptimeSemisync int64 `json:"uptimeSemisync"`
}

func (sla *Sla) Init() {
	sla.Uptime = 0
	sla.UptimeFailable = 0
	sla.UptimeSemisync = 0
	sla.Lasttime = time.Now().Unix()
	sla.Firsttime = sla.Lasttime
}

func (sla *Sla) GetUptime() float64 {
	return float64(100 * float64(sla.Uptime) / float64(sla.Lasttime-sla.Firsttime))
}

func (sla *Sla) GetUptimeSemiSync() float64 {
	return float64(100 * float64(sla.UptimeSemisync) / float64(sla.Lasttime-sla.Firsttime))
}

func (sla *Sla) GetUptimeFailable() float64 {
	return float64(100 * float64(sla.UptimeFailable) / float64(sla.Lasttime-sla.Firsttime))
}

func (sla *Sla) Format(f float64) string {
	up := strconv.FormatFloat(f, 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}

func (SM *StateMachine) GetSla() Sla {
	return SM.sla
}

func (SM *StateMachine) SetSla(mySla Sla) {
	SM.sla = mySla
}

func (SM *StateMachine) Init() {
	SM.CurState = NewMap()
	SM.OldState = NewMap()
	SM.CapturedState = new(sync.Map)
	SM.Discovered = false
	SM.sla.Init()
	SM.lastState = 0
	SM.heartbeats = 0
}

func (SM *StateMachine) SetMonitorSchemaState() {
	SM.Lock()
	SM.SchemaMonitorStartTime = time.Now().Unix()
	SM.InSchemaMonitor = true
	SM.Unlock()
}
func (SM *StateMachine) RemoveMonitorSchemaState() {
	SM.Lock()
	SM.InSchemaMonitor = false
	SM.SchemaMonitorEndTime = time.Now().Unix()
	SM.Unlock()
}

func (SM *StateMachine) SetFailoverState() {
	SM.Lock()
	SM.InFailover = true
	SM.Unlock()
}

func (SM *StateMachine) RemoveFailoverState() {
	SM.Lock()
	SM.InFailover = false
	SM.Unlock()
}

func (SM *StateMachine) IsInFailover() bool {
	return SM.InFailover
}

func (SM *StateMachine) IsInSchemaMonitor() bool {
	return SM.InSchemaMonitor
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
	return SM.sla.Format(SM.sla.GetUptime())
}
func (SM *StateMachine) GetUptimeSemiSync() string {
	return SM.sla.Format(SM.sla.GetUptimeSemiSync())
}

func (SM *StateMachine) ResetUptime() {
	SM.sla.Init()
}

func (SM *StateMachine) GetUptimeFailable() string {
	return SM.sla.Format(SM.sla.GetUptimeFailable())
}

func (SM *StateMachine) IsFailable() bool {

	SM.Lock()
	for _, value := range *SM.OldState {
		if value.ErrType == "ERROR" {
			SM.Unlock()
			return false
		}
	}
	SM.Discovered = true
	SM.Unlock()
	return true

}

func (SM *StateMachine) SetMasterUpAndSync(IsValidMaster bool, IsSemiSynced bool, IsNotDelay bool) {
	timenow := time.Now().Unix()
	if IsSemiSynced {
		SM.sla.UptimeSemisync = SM.sla.UptimeSemisync + (timenow - SM.sla.Lasttime)
	}
	if IsNotDelay {
		SM.sla.UptimeFailable = SM.sla.UptimeFailable + (timenow - SM.sla.Lasttime)
	}
	if IsValidMaster {
		SM.sla.Uptime = SM.sla.Uptime + (timenow - SM.sla.Lasttime)
	}
	SM.sla.Lasttime = timenow
	SM.heartbeats = SM.heartbeats + 1
	//fmt.Printf("INFO : is failable %b IsSemiSynced %b  IsNotDelay %b Uptime %d UptimeFailable %d UptimeSemisync %d\n",SM.IsFailable(),IsSemiSynced ,IsNotDelay, SM.Uptime, SM.UptimeFailable ,SM.UptimeSemisync)
}

func (SM *StateMachine) SetMasterUpAndSyncRestart() {
	timenow := time.Now().Unix()
	SM.sla.UptimeSemisync = SM.sla.UptimeSemisync + (timenow - SM.sla.Lasttime)
	SM.sla.UptimeFailable = SM.sla.UptimeFailable + (timenow - SM.sla.Lasttime)
	SM.sla.Uptime = SM.sla.Uptime + (timenow - SM.sla.Lasttime)
	SM.sla.Lasttime = timenow
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
	SM.Discovered = true
	SM.Unlock()
	return true
}

func (SM *StateMachine) UnDiscovered() {
	SM.Lock()
	SM.Discovered = false
	SM.Unlock()
}

func (SM *StateMachine) IsDiscovered() bool {
	return SM.Discovered
}

func (SM *StateMachine) GetStates() []string {
	var log []string

	//every thing in  OldState that can't be found in curstate
	for key2, value2 := range SM.GetLastResolvedStates() {
		log = append(log, fmt.Sprintf("RESOLV %s : %s", key2, value2.ErrDesc))
	}

	for key, value := range SM.GetLastOpenedStates() {
		log = append(log, fmt.Sprintf("OPENED %s : %s", key, value.ErrDesc))
	}

	return log
}

func (SM *StateMachine) GetFirstStates() []string {
	var log []string
	for key, value := range SM.GetLastOpenedStates() {
		log = append(log, fmt.Sprintf("OPENED %s : %s", key, value.ErrDesc))
	}

	return log
}

func (SM *StateMachine) GetLastResolvedStates() map[string]State {
	resolved := make(map[string]State)
	SM.Lock()
	//every thing in  OldState that can't be found in curstate
	for key, state := range *SM.OldState {
		if !SM.CurState.Search(key) {
			resolved[key] = state
		}
	}
	SM.Unlock()
	return resolved
}

func (SM *StateMachine) GetLastOpenedStates() map[string]State {
	opened := make(map[string]State)
	SM.Lock()
	//every thing in  OldState that can't be found in curstate
	for key, state := range *SM.CurState {
		if !SM.OldState.Search(key) {
			opened[key] = state
		}
	}
	SM.Unlock()
	return opened
}

func (SM *StateMachine) GetResolvedStates() []State {
	var log []State
	SM.Lock()
	for key, state := range *SM.OldState {
		if !SM.CurState.Search(key) {
			log = append(log, state)
		}
	}

	SM.Unlock()
	return log
}

func (SM *StateMachine) GetOpenStates() []State {
	var log []State
	SM.Lock()
	for _, state := range *SM.CurState {
		log = append(log, state)
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

func (SM *StateMachine) PreserveState(key string) {
	if SM.OldState.Search(key) {
		value := (*SM.OldState)[key]
		SM.AddState(key, value)
	}
}

func (SM *StateMachine) AddToCapturedState(key string, cstate *CapturedState) {
	_, ok := SM.CapturedState.Load(key)
	if !ok {
		SM.CapturedState.Store(key, cstate)
	}
}

func (SM *StateMachine) DeleteCapturedState(key string) {
	SM.CapturedState.Delete(key)
}

func (SM *StateMachine) SearchCapturedState(key string) bool {
	_, ok := SM.CapturedState.Load(key)
	if ok {
		return true
	} else {
		return false
	}
}

func (SM *StateMachine) GetCapturedState(key string) (*CapturedState, bool) {
	cs, ok := SM.CapturedState.Load(key)
	if ok {
		return cs.(*CapturedState), true
	} else {
		return nil, false
	}
}
