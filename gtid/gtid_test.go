package gtid

import "testing"

func TestGtid(t *testing.T) {
	gtids := "0-1-100,1-2-101"
	list := NewList(gtids)
	domains := list.GetDomainIDs()
	if domains[0] != 0 || domains[1] != 1 {
		t.Error("Domains should be {0,1}")
	}
	servers := list.GetServerIDs()
	if servers[0] != 1 || servers[1] != 2 {
		t.Error("Servers should be {1,2}")
	}
	seqnos := list.GetSeqNos()
	if seqnos[0] != 100 || seqnos[1] != 101 {
		t.Error("Sequences should be {100,101}")
	}
}
