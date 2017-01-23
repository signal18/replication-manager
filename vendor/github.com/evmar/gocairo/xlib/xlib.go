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

package xlib

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
*/
import "C"

import (
	"unsafe"

	"github.com/martine/gocairo/cairo"
)

type Callbacks interface {
	Draw(*cairo.Context, *cairo.XlibSurface)
}

type Window struct {
	dpy *C.Display
	xw  C.Window
}

// Note that stringer doesn't work in the presence of C types.  I worked
// around this by just commenting out all the code except for XEventType
// when running "go generate".

//go:generate stringer -type=XEventType
type XEventType int

const (
	KeyPress         XEventType = 2
	KeyRelease       XEventType = 3
	ButtonPress      XEventType = 4
	ButtonRelease    XEventType = 5
	MotionNotify     XEventType = 6
	EnterNotify      XEventType = 7
	LeaveNotify      XEventType = 8
	FocusIn          XEventType = 9
	FocusOut         XEventType = 10
	KeymapNotify     XEventType = 11
	Expose           XEventType = 12
	GraphicsExpose   XEventType = 13
	NoExpose         XEventType = 14
	VisibilityNotify XEventType = 15
	CreateNotify     XEventType = 16
	DestroyNotify    XEventType = 17
	UnmapNotify      XEventType = 18
	MapNotify        XEventType = 19
	MapRequest       XEventType = 20
	ReparentNotify   XEventType = 21
	ConfigureNotify  XEventType = 22
	ConfigureRequest XEventType = 23
	GravityNotify    XEventType = 24
	ResizeRequest    XEventType = 25
	CirculateNotify  XEventType = 26
	CirculateRequest XEventType = 27
	PropertyNotify   XEventType = 28
	SelectionClear   XEventType = 29
	SelectionRequest XEventType = 30
	SelectionNotify  XEventType = 31
	ColormapNotify   XEventType = 32
	ClientMessage    XEventType = 33
	MappingNotify    XEventType = 34
	GenericEvent     XEventType = 35
)

func XMain(callbacks Callbacks) {
	dpy := C.XOpenDisplay(nil)

	w := C.XCreateSimpleWindow(dpy, C.XDefaultRootWindow(dpy),
		0, 0, 600, 400,
		0, 0, 0)
	C.XSelectInput(dpy, w, C.StructureNotifyMask|C.SubstructureNotifyMask|C.ExposureMask)
	C.XMapWindow(dpy, w)

	win := Window{dpy: dpy, xw: w}
	visual := C.XDefaultVisual(dpy, 0)

	surf := cairo.XlibSurfaceCreate(unsafe.Pointer(dpy), uint64(win.xw), unsafe.Pointer(visual), 10, 10)

	for {
		var e C.XEvent
		C.XNextEvent(dpy, &e)
		typ := XEventType(*(*C.int)(unsafe.Pointer(&e)))
		// log.Printf("X event: %s", typ)
		switch typ {
		case C.ConfigureNotify:
			e := (*C.XConfigureEvent)(unsafe.Pointer(&e))
			surf.SetSize(int(e.width), int(e.height))
		case C.Expose:
			cr := cairo.Create(surf.Surface)
			callbacks.Draw(cr, surf)
		default:
			// log.Printf("unknown X event %s", typ)
		}
	}
}
