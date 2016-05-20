package main

import (
	"encoding/binary"
	"os"
)

type stateFile struct {
	Handle    *os.File
	Name      string
	Count     int32
	Timestamp int64
}

func newStateFile(name string) *stateFile {
	sf := new(stateFile)
	sf.Name = name
	return sf
}

func (sf *stateFile) create() error {
	var err error
	sf.Handle, err = os.Create(sf.Name)
	if err != nil {
		return err
	}
	return nil
}

func (sf *stateFile) write() error {
	err := binary.Write(sf.Handle, binary.LittleEndian, sf.Count)
	if err != nil {
		return err
	}
	err = binary.Write(sf.Handle, binary.LittleEndian, sf.Timestamp)
	if err != nil {
		return err
	}
	return nil
}

func (sf *stateFile) read() error {
	sf.Handle.Seek(0, 0)
	err := binary.Read(sf.Handle, binary.LittleEndian, &sf.Count)
	if err != nil {
		return err
	}
	err = binary.Read(sf.Handle, binary.LittleEndian, &sf.Timestamp)
	if err != nil {
		return err
	}
	return nil
}
