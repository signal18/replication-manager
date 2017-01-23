package stalecucumber

import (
	"strings"
	"testing"
)

func TestFuzzCrashers(t *testing.T) {

	var crashers = []string{
		"}}(s",       //protocol_0 SETITEM hash of unhashable
		"((d}d",      //protocol_0.go opcode_DICT hash of unhashable
		"}(}(a}u",    //protocol_1 SETITEMS hash of unhashable
		"(p0\nj0000", //pickle_machine flushMemoBuffer index out of range
	}

	for _, f := range crashers {
		Unpickle(strings.NewReader(f))
	}
}
