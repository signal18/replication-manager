package state

import "log"

type State struct {
	ErrType    string
	ErrDesc    string
	ErrPrinted bool
}

type Map map[string]State

func NewMap() *Map {
	m := make(Map)
	return &m
}

func (m Map) Log(key string) {
	s := m[key]
	if s.ErrPrinted == false {
		log.Println(s.ErrType, ":", key, s.ErrDesc)
		// TODO: FIX tlog.Add(fmt.Sprintf(s.errType, ":", key, s.errDesc))
		s.ErrPrinted = true
		m[key] = s
	}
}

func (m Map) Add(key string, s State) {
	_, ok := m[key]
	if !ok {
		m[key] = s

	}
	m.Log(key)
}

// Clear copies the current map to argument map and clears it
func (m *Map) Clear() *Map {
	o := m
	n := NewMap()
	m = n
	return o
}
