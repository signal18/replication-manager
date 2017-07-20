// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package gtid

import (
	"fmt"
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
	if s == "" {
		return gl
	}
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

// GetDomainIDs returns a slice of domain ID integers
func (gl List) GetDomainIDs() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.DomainID)
	}
	return d
}

// GetServerIDs returns a slice of server ID integers
func (gl List) GetServerIDs() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.ServerID)
	}
	return d
}

// GetSeqNos returns a slice of sequence integers
func (gl List) GetSeqNos() []uint64 {
	var d []uint64
	for _, g := range gl {
		d = append(d, g.SeqNo)
	}
	return d
}

// return the sequence of a sprecific domain
func (gl List) GetSeqServerIdNos(serverId uint64) uint64 {
	for _, g := range gl {
		if g.ServerID == serverId {
			return g.SeqNo
		}
	}
	return 0
}

// Sprint returns a formatted GTID List string
func (gl List) Sprint() string {
	var sl []string
	for _, g := range gl {
		s := fmt.Sprintf("%d-%d-%d", g.DomainID, g.ServerID, g.SeqNo)
		sl = append(sl, s)
	}
	return strings.Join(sl, ",")
}

func (gl List) Equal(glcomp List) bool {
	//	var sl []string
	//	for _, g := range gl {

	//	}
	return true
}
