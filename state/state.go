package state

import "fmt"
import "time"
import "strconv"

type State struct {
	ErrType string
	ErrDesc string
	ErrFrom string
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

func (m Map) Search(key string) bool {
	_, ok := m[key]
	if ok {
		return true
	} else {
		return false
	}

}

type StateMachine struct {
	curState            *Map
	oldState            *Map
	discovered          bool
	lasttime            int64
	firsttime           int64
	uptime              int64
	uptimeFailable      int64
	uptimeSemisync      int64
	avgReplicationDelay float32
}

func (SM *StateMachine) Init() {

	SM.curState = NewMap()
	SM.oldState = NewMap()
	SM.discovered = false
	SM.lasttime = time.Now().Unix()
	SM.firsttime = SM.lasttime
	SM.uptime = 0
	SM.uptimeFailable = 0
	SM.uptimeSemisync = 0
}

func (SM *StateMachine) AddState(key string, s State) {
	SM.curState.Add(key, s)
}

func (SM *StateMachine) GetUptime() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.uptime)/float64(time.Now().Unix()-SM.firsttime)), 'f', 5, 64)
	//fmt.Printf("INFO : Uptime %f", float64(SM.uptime)/float64(time.Now().Unix()- SM.firsttime))
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}
func (SM *StateMachine) GetUptimeSemiSync() string {

	var up = strconv.FormatFloat(float64(100*float64(SM.uptimeSemisync)/float64(time.Now().Unix()-SM.firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}
func (SM *StateMachine) GetUptimeFailable() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.uptimeFailable)/float64(time.Now().Unix()-SM.firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}

func (SM *StateMachine) SetMasterUpAndSync(IsSemiSynced bool, IsDelay bool) {
	var timenow int64
	timenow = time.Now().Unix()
	if IsSemiSynced {
		SM.uptime = SM.uptime + (timenow - SM.lasttime)
		SM.uptimeFailable = SM.uptimeFailable + (timenow - SM.lasttime)
		SM.uptimeSemisync = SM.uptimeSemisync + (timenow - SM.lasttime)
	} else if IsDelay {
		SM.uptime = SM.uptime + (timenow - SM.lasttime)
		SM.uptimeFailable = SM.uptimeFailable + (timenow - SM.lasttime)
	} else {
		SM.uptime = SM.uptime + (timenow - SM.lasttime)
	}
	SM.lasttime = timenow

	//   fmt.Printf("INFO : Uptime %b  %b %d %d %d\n",IsSemiSynced ,IsDelay, SM.uptime, SM.uptimeFailable ,SM.uptimeSemisync)
}

// Clear copies the current map to argument map and clears it
func (SM *StateMachine) ClearState() {
	SM.oldState = SM.curState
	SM.curState = nil
	SM.curState = NewMap()

}

func (SM *StateMachine) CanMonitor() bool {

	for _, value := range *SM.curState {
		if value.ErrType == "ERROR" {
			return false
		}
	}
	SM.discovered = true
	return true

}

func (SM *StateMachine) IsDiscovered() bool {

	return SM.discovered

}

func (SM *StateMachine) GetState() []string {

	var log []string
	for key2, value2 := range *SM.oldState {
		if SM.curState.Search(key2) == false {
			log = append(log, fmt.Sprintf("%s:%s HAS BEEN FIXED, %s", value2.ErrType, key2, value2.ErrDesc))

		}
	}

	for key, value := range *SM.curState {
		if SM.oldState.Search(key) == false {
			log = append(log, fmt.Sprintf("%s:%s %s", value.ErrType, key, value.ErrDesc))

		}
	}
	return log
}
