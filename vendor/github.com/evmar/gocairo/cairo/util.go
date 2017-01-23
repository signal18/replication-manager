// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cairo

import (
	"io"
	"reflect"
	"sync"
	"unsafe"
)

import "C"

// sliceBytes returns a pointer to the bytes of the data in a slice.
func sliceBytes(p unsafe.Pointer) unsafe.Pointer {
	hdr := (*reflect.SliceHeader)(p)
	return unsafe.Pointer(hdr.Data)
}

// toError converts a Status into a Go error.
func (s Status) toError() error {
	if s == StatusSuccess {
		return nil
	}
	return s
}

// In Go 1.6, you're not allowed to pass Go pointers through C.
// To work around this, use a map keyed by integers for stashing
// arbitrary Go data.
type goPointerStash struct {
	sync.Mutex
	data    map[C.int]interface{}
	nextKey C.int
}

var goPointers = &goPointerStash{}

func (gp *goPointerStash) put(data interface{}) C.int {
	gp.Lock()
	defer gp.Unlock()
	if gp.data == nil {
		gp.data = make(map[C.int]interface{})
	}
	key := gp.nextKey
	gp.nextKey++
	gp.data[key] = data
	return key
}

func (gp *goPointerStash) get(key C.int) interface{} {
	gp.Lock()
	defer gp.Unlock()
	return gp.data[key]
}

func (gp *goPointerStash) clear(key C.int) {
	gp.Lock()
	defer gp.Unlock()
	delete(gp.data, key)
}

type writeClosure struct {
	w   io.Writer
	err error
}

//export gocairoWriteFunc
func gocairoWriteFunc(key C.int, data unsafe.Pointer, clength C.uint) bool {
	writeClosure := goPointers.get(key).(writeClosure)
	length := uint(clength)
	slice := ((*[1 << 30]byte)(data))[:length:length]
	_, writeClosure.err = writeClosure.w.Write(slice)
	return writeClosure.err == nil
}

type readClosure struct {
	r   io.Reader
	err error
}

//export gocairoReadFunc
func gocairoReadFunc(key C.int, data unsafe.Pointer, clength C.uint) bool {
	readClosure := goPointers.get(key).(readClosure)
	length := uint(clength)
	buf := ((*[1 << 30]byte)(data))[:length:length]
	_, readClosure.err = io.ReadFull(readClosure.r, buf)
	return readClosure.err == nil
}
