package main

import (
	"testing"
	"time"
)

func TestStateFile(t *testing.T) {
	sf := newStateFile("/tmp/mrm.state")
	err := sf.access()
	if err != nil {
		t.Fatal("Error creating file: ", err)
	}
	sf.Count = 3
	time := time.Now()
	sf.Timestamp = time.Unix()
	err = sf.write()
	if err != nil {
		t.Fatal("Error writing bytes: ", err)
	}
	err = sf.read()
	if err != nil {
		t.Fatal("Error reading bytes: ", err)
	}
	if sf.Count != 3 {
		t.Fatalf("Read count %d, expected 3", sf.Count)
	}
	t.Log("Read timestamp", sf.Timestamp)
}
