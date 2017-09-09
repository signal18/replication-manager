// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package gtid

import (
	"fmt"
	"sort"
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

type lessFunc func(p1, p2 *Gtid) bool

// multiSorter implements the Sort interface, sorting the changes within.
type multiSorter struct {
	gtids []Gtid
	less  []lessFunc
}

// Sort sorts the argument slice according to the less functions passed to OrderedBy.
func (ms *multiSorter) Sort(gtids []Gtid) {
	ms.gtids = gtids
	sort.Sort(ms)
}

// OrderedBy returns a Sorter that sorts using the less functions, in order.
// Call its Sort method to sort the data.
func OrderedBy(less ...lessFunc) *multiSorter {
	return &multiSorter{
		less: less,
	}
}

// Len is part of sort.Interface.
func (ms *multiSorter) Len() int {
	return len(ms.gtids)
}

// Swap is part of sort.Interface.
func (ms *multiSorter) Swap(i, j int) {
	ms.gtids[i], ms.gtids[j] = ms.gtids[j], ms.gtids[i]
}

// Less is part of sort.Interface. It is implemented by looping along the
// less functions until it finds a comparison that is either Less or
// !Less. Note that it can call the less functions twice per call. We
// could change the functions to return -1, 0, 1 and reduce the
// number of calls for greater efficiency: an exercise for the reader.
func (ms *multiSorter) Less(i, j int) bool {
	p, q := &ms.gtids[i], &ms.gtids[j]
	// Try all but the last comparison.
	var k int
	for k = 0; k < len(ms.less)-1; k++ {
		less := ms.less[k]
		switch {
		case less(p, q):
			// p < q, so we have a decision.
			return true
		case less(q, p):
			// p > q, so we have a decision.
			return false
		}
		// p == q; try the next comparison.
	}
	// All comparisons to here said "equal", so just return whatever
	// the final comparison reports.
	return ms.less[k](p, q)
}

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

func (gl List) Equal(glcomp *List) bool {
	server := func(c1, c2 *Gtid) bool {
		return c1.ServerID < c2.ServerID
	}
	domain := func(c1, c2 *Gtid) bool {
		return c1.DomainID < c2.DomainID
	}
	OrderedBy(domain, server).Sort(gl)
	OrderedBy(domain, server).Sort(*glcomp)
	if gl.Sprint() == glcomp.Sprint() {
		return true

	}
	return false
}
