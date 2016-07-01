package state

import "fmt"
import "time"

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
	curState   *Map
	oldState   *Map
	discovered bool
	lasttime int64
	firsttime int64
	uptime int64
	uptime_failable int64
	uptime_semisync int64
	avg_replication_delay float32

}

func (SM *StateMachine) Init() {
	
	SM.curState = NewMap()
	SM.oldState = NewMap()
	SM.discovered = false
	SM.lasttime = time.Now().Unix()
	SM.firsttime = SM.lasttime
	SM.uptime = 0 
	SM.uptime_failable = 0
	SM.uptime_semisync = 0
}

func (SM *StateMachine) AddState(key string, s State) {
	SM.curState.Add(key, s)
}

 


func (SM *StateMachine) SetMasterUpAndSync(IsSemiSynced bool , IsDelay bool) {
      var timenow int64
      timenow =  time.Now().Unix()
      if IsSemiSynced {
        SM.uptime = SM.lasttime - timenow
        SM.uptime_failable = SM.lasttime - timenow
        SM.uptime_semisync =  SM.lasttime - timenow
      } else if IsDelay  {
		SM.uptime = SM.lasttime - timenow
        SM.uptime_failable = SM.lasttime - timenow
      } else {
  	    SM.uptime = SM.lasttime - timenow	
	  }
      SM.lasttime = timenow
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
