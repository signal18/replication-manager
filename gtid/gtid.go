package gtid

import (
	"strconv"
	"strings"
)

// Gtid defines a GTID object
type Gtid struct {
	DomainID uint64
	ServerID uint64
	SeqNo    uint64
}

// List defines a slice of GTIDs
type List []Gtid

// NewList returns a slice of GTIDs from a string
// Usually it shouldn't be called directly
func NewList(s string) *List {
	gl := new(List)
	l := strings.Split(s, ",")
	for _, g := range l {
		gtid := NewGtid(g)
		*gl = append(*gl, *gtid)
	}
	return gl
}

// NewGtid returns a new Gtid from a string
func NewGtid(s string) *Gtid {
	g := new(Gtid)
	e := strings.Split(s, "-")
	g.DomainID, _ = strconv.ParseUint(e[0], 10, 32)
	g.ServerID, _ = strconv.ParseUint(e[1], 10, 32)
	g.SeqNo, _ = strconv.ParseUint(e[2], 10, 64)
	return g
}

func (gl List) GetDomainIDs() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.DomainID)
	}
	return d
}

func (gl List) GetServerIDs() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.ServerID)
	}
	return d
}

func (gl List) GetSeqNos() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.SeqNo)
	}
	return d
}
