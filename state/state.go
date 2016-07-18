package state

import "fmt"
import "time"
import "strconv"
import "sync"


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
	CurState            *Map
	OldState            *Map
	discovered          bool
	lasttime            int64
	firsttime           int64
	uptime              int64
	uptimeFailable      int64
	uptimeSemisync      int64
  lastState 					int64
	avgReplicationDelay float32
	sync.Mutex
}

func (SM *StateMachine) Init() {

	SM.CurState = NewMap()
	SM.OldState = NewMap()
	SM.discovered = false
	SM.lasttime = time.Now().Unix()
	SM.firsttime = SM.lasttime
	SM.uptime = 0
	SM.uptimeFailable = 0
	SM.uptimeSemisync = 0
  SM.lastState = 0
}

func (SM *StateMachine) AddState(key string, s State) {
  SM.Lock()
	SM.CurState.Add(key, s)
	SM.Unlock()
}

func (SM *StateMachine) GetUptime() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.uptime)/float64(SM.lasttime-SM.firsttime)), 'f', 5, 64)
	//fmt.Printf("INFO : Uptime %f", float64(SM.uptime)/float64(time.Now().Unix()- SM.firsttime))
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}
func (SM *StateMachine) GetUptimeSemiSync() string {

	var up = strconv.FormatFloat(float64(100*float64(SM.uptimeSemisync)/float64(SM.lasttime-SM.firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}
func (SM *StateMachine) GetUptimeFailable() string {
	var up = strconv.FormatFloat(float64(100*float64(SM.uptimeFailable)/float64(SM.lasttime-SM.firsttime)), 'f', 5, 64)
	if up == "100.00000" {
		up = "99.99999"
	}
	return up
}

func (SM *StateMachine) IsFailable() bool {
 return SM.CanMonitor()
}

func (SM *StateMachine) SetMasterUpAndSync(IsSemiSynced bool, IsNotDelay bool) {
	var timenow int64
	timenow = time.Now().Unix()
	if IsSemiSynced == true && SM.IsFailable() == true {
	  SM.uptimeSemisync = SM.uptimeSemisync + (timenow - SM.lasttime)
	}
	if IsNotDelay==true && SM.IsFailable() == true {
		SM.uptimeFailable = SM.uptimeFailable + (timenow - SM.lasttime)
	}
  if SM.IsFailable() == true 	{
		SM.uptime = SM.uptime + (timenow - SM.lasttime)
	}
	SM.lasttime = timenow

  //fmt.Printf("INFO : is failable %b IsSemiSynced %b  IsNotDelay %b uptime %d uptimeFailable %d uptimeSemisync %d\n",SM.IsFailable(),IsSemiSynced ,IsNotDelay, SM.uptime, SM.uptimeFailable ,SM.uptimeSemisync)
}

// Clear copies the current map to argument map and clears it
func (SM *StateMachine) ClearState() {
	SM.Lock()
	SM.OldState = SM.CurState
	SM.CurState = nil
	SM.CurState = NewMap()
  SM.Unlock()
}

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

func (SM *StateMachine) IsDiscovered() bool {

	return SM.discovered

}

func (SM *StateMachine) GetState() []string {

	var log []string
	for key2, value2 := range *SM.OldState {
		if SM.CurState.Search(key2) == false {
			log = append(log, fmt.Sprintf("%s: %s HAS BEEN FIXED, %s", value2.ErrType, key2, value2.ErrDesc))

		}
	}

	for key, value := range *SM.CurState {
		if SM.OldState.Search(key) == false {
			log = append(log, fmt.Sprintf("%s: %s %s", value.ErrType, key, value.ErrDesc))

		}
	}
	return log
}
