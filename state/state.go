package state

import "log"

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

type StateMachine struct {
  curState *Map
  oldState *Map
}

 func (SM *StateMachine) Init()  {
	 SM.curState = NewMap( )
	 SM.oldState  = NewMap()
  	
}

func (SM *StateMachine) AddSate(key string, s State)  {
	SM.curState.Add(key, s )
	 
}

// Clear copies the current map to argument map and clears it
func (SM *StateMachine) ClearSate()  {
	SM.oldState = SM.curState
	n := NewMap()
	SM.curState = n
	 
}

func (SM *StateMachine) LogState( ) {
	  for key, value := range SM.curState {

	   log.Println(value.ErrType, ":", key, value.ErrDesc)
	// TODO: FIX tlog.Add(fmt.Sprintf(s.errType, ":", key, s.errDesc))
	} 
}


