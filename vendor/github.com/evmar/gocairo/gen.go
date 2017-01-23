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

package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"go/format"
	"log"
	"os"
	"os/exec"
	"strings"

	"rsc.io/c2go/cc"
)

const cDocUrl = "http://cairographics.org/manual/"

// intentionalSkip maps C names to the reason why they're left out
// when we intentionally don't generate bindings for them.
var intentionalSkip = map[string]string{
	"cairo_bool_t":           "mapped to bool",
	"cairo_user_data_key_t":  "type only used as a placeholder in C",
	"cairo_matrix_init":      "just the same thing as creating the struct yourself",
	"cairo_status_to_string": "mapped to the error interface, use .Error()",

	"cairo_surface_write_to_png":        "specially implemented to work with io.Writer",
	"cairo_surface_write_to_png_stream": "specially implemented to work with io.Writer",

	"cairo_image_surface_create_from_png":        "specially implemented to work with io.Reader",
	"cairo_image_surface_create_from_png_stream": "specially implemented to work with io.Reader",

	"cairo_glyph_allocate": "manage memory on the Go side",
	"cairo_glyph_free":     "manage memory on the Go side",

	"cairo_path_data_t": "used internally in path iteration",

	"cairo_debug_reset_static_data": "intended for use with valgrind, requires deterministic object destruction",

	// These are fake types defined in fake-xlib.h.
	"Drawable": "",
	"Pixmap":   "",
	"Display":  "",
	"Visual":   "",
	"Screen":   "",
}

// skipUnhandled maps C names to the excuse why we haven't wrapped them yet.
var skipUnhandled = map[string]string{
	"cairo_pattern_get_rgba":                   "mix of out params and status",
	"cairo_pattern_get_color_stop_rgba":        "mix of out params and status",
	"cairo_pattern_get_color_stop_count":       "mix of out params and status",
	"cairo_pattern_get_linear_points":          "mix of out params and status",
	"cairo_pattern_get_radial_circles":         "mix of out params and status",
	"cairo_mesh_pattern_get_patch_count":       "mix of out params and status",
	"cairo_mesh_pattern_get_corner_color_rgba": "mix of out params and status",
	"cairo_mesh_pattern_get_control_point":     "mix of out params and status",

	"cairo_scaled_font_text_to_glyphs": "fancy font APIs",
	"cairo_surface_get_mime_data":      "mime functions",
	"cairo_surface_set_mime_data":      "mime functions",
	"cairo_pattern_get_surface":        "need to figure out refcounting",
}

var typeTodoList = map[string]string{
	"cairo_rectangle_int_t":  "hard to wrap API",
	"cairo_rectangle_list_t": "hard to wrap API",

	// Fancy font APIs -- TODO.
	"cairo_text_cluster_t": "needs work",

	// Raster sources -- TODO.
	"cairo_raster_source_acquire_func_t":  "callbacks",
	"cairo_raster_source_snapshot_func_t": "callbacks",
	"cairo_raster_source_copy_func_t":     "callbacks",
	"cairo_raster_source_finish_func_t":   "callbacks",
}

var manualImpl = map[string]string{
	"cairo_image_surface_get_data": `func (i *ImageSurface) Data() []byte {
	buf := C.cairo_image_surface_get_data(i.Ptr)
	return C.GoBytes(unsafe.Pointer(buf), C.int(i.GetStride()*i.GetHeight()))
}`,

	"cairo_svg_get_versions": `func SVGGetVersions() []SVGVersion {
var cVersionsPtr *C.cairo_svg_version_t
var cNumVersions C.int
C.cairo_svg_get_versions(&cVersionsPtr, &cNumVersions)
slice := (*[1<<30]C.cairo_svg_version_t)(unsafe.Pointer(cVersionsPtr))[:cNumVersions:cNumVersions]
versions := make([]SVGVersion, cNumVersions)
for i := 0; i < int(cNumVersions); i++ {
versions[i] = SVGVersion(slice[i])
}
return versions
}`,
}

// outParams maps a function name to a per-parameter bool of whether it's
// an output-only param.
var outParams = map[string][]bool{
	"cairo_clip_extents":                  {false, true, true, true, true},
	"cairo_fill_extents":                  {false, true, true, true, true},
	"cairo_path_extents":                  {false, true, true, true, true},
	"cairo_stroke_extents":                {false, true, true, true, true},
	"cairo_recording_surface_ink_extents": {false, true, true, true, true},

	"cairo_get_current_point":               {false, true, true},
	"cairo_surface_get_device_scale":        {false, true, true},
	"cairo_surface_get_device_offset":       {false, true, true},
	"cairo_surface_get_fallback_resolution": {false, true, true},

	// TODO
	// "cairo_pattern_get_rgba":            {false, true, true, true, true},
	// "cairo_pattern_get_color_stop_rgba": {false, false, true, true, true, true, true},
	// "cairo_pattern_get_color_stop_count": {false, true},
}

var arrayParams = map[string]int{
	"cairo_set_dash": 1,

	"cairo_show_glyphs":               1,
	"cairo_glyph_path":                1,
	"cairo_glyph_extents":             1,
	"cairo_scaled_font_glyph_extents": 1,
}

// sharedTypes has the Go type for C types where we just cast a
// pointer across directly.
var sharedTypes = map[string]string{
	"double": "float64",
	// More structs are added as we parse the header.
}

var subTypes = []struct {
	sub, super string
}{
	{"ImageSurface", "Surface"},
	{"RecordingSurface", "Surface"},
	{"SurfaceObserver", "Surface"},
	{"ToyFontFace", "FontFace"},
	{"MeshPattern", "Pattern"},

	{"SVGSurface", "Surface"},

	{"XlibSurface", "Surface"},
	{"XlibDevice", "Device"},
}

var rawCTypes = map[string]bool{
	"Display":  true,
	"Drawable": true,
	"Visual":   true,
	"Pixmap":   true,
	"Screen":   true,
}

// acronyms are substrings that should be all caps or all lowercase.
var acronyms = map[string]bool{
	"argb":   true,
	"argb32": true,
	"bgr":    true,
	"cogl":   true,
	"ctm":    true,
	"drm":    true,
	"gl":     true,
	"os2":    true,
	"pdf":    true,
	"png":    true,
	"ps":     true,
	"rgb":    true,
	"rgb16":  true,
	"rgb24":  true,
	"rgb30":  true,
	"rgba":   true,
	"svg":    true,
	"vbgr":   true,
	"vg":     true,
	"vrgb":   true,
	"xcb":    true,
	"xml":    true,
	"xor":    true,
}

type Writer struct {
	bytes.Buffer
	links map[string]string
}

func (w *Writer) Print(format string, a ...interface{}) {
	fmt.Fprintf(w, format+"\n", a...)
}

func (w *Writer) Source() []byte {
	src, err := format.Source(w.Bytes())
	if err != nil {
		log.Printf("gofmt failed: %s", err)
		log.Printf("using unformatted source to enable debugging")
		return w.Bytes()
	}
	return src
}

func cNameToGo(name string, upper bool) string {
	switch name {
	case "int":
		return name
	case "double":
		return "float64"
	case "ulong":
		return "uint32"
	case "uint":
		// This is used in contexts where int is fine.
		return "int"
	case "cairo_t":
		return "Context"
	}

	parts := strings.Split(name, "_")
	out := ""
	for _, p := range parts {
		switch p {
		case "cairo", "t":
			// skip
		default:
			if upper || out != "" {
				if acronyms[p] {
					out += strings.ToUpper(p)
				} else {
					out += strings.Title(p)
				}
			} else {
				out += p
			}
		}
	}
	return out
}

func cNameToGoUpper(name string) string {
	return cNameToGo(name, true)
}

func cNameToGoLower(name string) string {
	return cNameToGo(name, false)
}

type typeMap struct {
	goType string
	cToGo  func(in string) string
	goToC  func(in string) (string, string)
	method string
}

func cTypeToMap(typ *cc.Type) *typeMap {
	switch typ.Kind {
	case cc.Ptr:
		str := typ.Base.String()
		switch str {
		case "char":
			return &typeMap{
				goType: "string",
				cToGo: func(in string) string {
					return fmt.Sprintf("C.GoString(%s)", in)
				},
				goToC: func(in string) (string, string) {
					cvar := fmt.Sprintf("c_%s", in)
					return cvar, fmt.Sprintf("%s := C.CString(%s); defer C.free(unsafe.Pointer(%s))", cvar, in, cvar)
				},
			}
		case "uchar", "void":
			log.Printf("TODO %s: in type blacklist (TODO: add reasoning)", str)
			return nil
		}

		if goType, ok := sharedTypes[str]; ok {
			// TODO: it appears *Rectangle might only be used for out params.
			return &typeMap{
				goType: "*" + goType,
				cToGo: func(in string) string {
					return fmt.Sprintf("(*%s)(unsafe.Pointer(%s))", goType, in)
				},
				goToC: func(in string) (string, string) {
					return fmt.Sprintf("(*C.%s)(unsafe.Pointer(%s))", str, in), ""
				},
				method: goType,
			}
		}

		if rawCTypes[str] {
			return &typeMap{
				goType: "unsafe.Pointer",
				cToGo: func(in string) string {
					return fmt.Sprintf("unsafe.Pointer(%s)", in)
				},
				goToC: func(in string) (string, string) {
					return fmt.Sprintf("(*C.%s)(%s)", str, in), ""
				},
			}
		}

		goName := cNameToGoUpper(str)
		if reason, ok := typeTodoList[str]; ok {
			log.Printf("TODO %s: %s", str, reason)
			return nil
		}
		return &typeMap{
			goType: "*" + goName,
			cToGo: func(in string) string {
				return fmt.Sprintf("wrap%s(%s)", goName, in)
			},
			goToC: func(in string) (string, string) {
				return fmt.Sprintf("%s.Ptr", in), ""
			},
			method: goName,
		}
	case cc.Void:
		return &typeMap{
			goType: "",
			cToGo:  nil,
			goToC:  nil,
		}
	}

	// Otherwise, it's a basic non-pointer type.
	cName := typ.String()
	if reason, ok := typeTodoList[cName]; ok {
		log.Printf("TODO %s: %s", cName, reason)
		return nil
	}

	switch cName {
	case "cairo_bool_t":
		return &typeMap{
			goType: "bool",
			cToGo: func(in string) string {
				return fmt.Sprintf("%s != 0", in)
			},
			goToC: func(in string) (string, string) {
				return fmt.Sprintf("C.%s(%s)", cName, in), ""
			},
		}
	case "cairo_status_t":
		return &typeMap{
			goType: "error",
			cToGo: func(in string) string {
				return fmt.Sprintf("Status(%s).toError()", in)
			},
			goToC: nil,
		}
	case "Drawable", "Pixmap":
		return &typeMap{
			goType: "uint64",
			cToGo: func(in string) string {
				return fmt.Sprintf("uint64(%s)", in)
			},
			goToC: func(in string) (string, string) {
				return fmt.Sprintf("C.%s(%s)", cName, in), ""
			},
		}
	}

	goName := cNameToGoUpper(cName)
	m := &typeMap{
		goType: goName,
		cToGo: func(in string) string {
			return fmt.Sprintf("%s(%s)", goName, in)
		},
		goToC: func(in string) (string, string) {
			return fmt.Sprintf("C.%s(%s)", cName, in), ""
		},
	}
	if goName == "Format" {
		// Attempt to put methods on our "Format" type.
		m.method = goName
	}
	if goName == "SVGVersion" {
		m.method = goName
	}
	return m
}

func (w *Writer) writeDocString(name, extra string) {
	w.Print("// See %s%s.", name, extra)
	if link, ok := w.links[name]; ok {
		w.Print("//")
		w.Print("// C API documentation: %s%s", cDocUrl, link)
	}
}

func (w *Writer) genTypeDef(d *cc.Decl) {
	w.writeDocString(d.Name, "")
	goName := cNameToGoUpper(d.Name)

	switch d.Type.Kind {
	case cc.Struct:
		if d.Type.Decls == nil || goName == "Path" {
			// Opaque typedef.
			w.Print(`type %s struct {
Ptr *C.%s
}`, goName, d.Name)

			cFinalizer := d.Name
			if strings.HasSuffix(cFinalizer, "_t") {
				cFinalizer = cFinalizer[:len(cFinalizer)-2]
			}
			cFinalizer += "_destroy"
			w.Print("func free%s(obj *%s) {", goName, goName)
			w.Print("C.%s(obj.Ptr)", cFinalizer)
			w.Print("}")

			w.Print("func wrap%s(p *C.%s) *%s {", goName, d.Name, goName)
			w.Print("ret := &%s{p}", goName)
			w.Print("runtime.SetFinalizer(ret, free%s)", goName)
			w.Print("return ret")
			w.Print("}")

			w.Print("// Wrap a C %s* found from some external source as a *%s.  The Go side will destroy the reference when it's no longer used.", d.Name, goName)
			w.Print("func Wrap%s(p unsafe.Pointer) *%s {", goName, goName)
			w.Print("return wrap%s((*C.%s)(p))", goName, d.Name)
			w.Print("}")

			w.Print("// Construct a %s from a C %s* found from some exernal source.  It is the caller's responsibility to ensure the pointer lives.", goName, d.Name)
			w.Print("func Borrow%s(p unsafe.Pointer) *%s {", goName, goName)
			w.Print("return &%s{(*C.%s)(p)}", goName, d.Name)
			w.Print("}")
		} else {
			sharedTypes[d.Name] = goName
			w.Print("type %s struct {", goName)
			for _, d := range d.Type.Decls {
				typ := cTypeToMap(d.Type)
				w.Print("%s %s", cNameToGoUpper(d.Name), typ.goType)
			}
			w.Print("}")
		}
	case cc.Enum:
		type constEntry struct {
			goName, cName string
		}
		consts := make([]constEntry, 0, len(d.Type.Decls))
		for _, d := range d.Type.Decls {
			constName := d.Name
			if strings.HasPrefix(constName, "CAIRO_") {
				constName = constName[len("CAIRO_"):]
			}
			constName = cNameToGoUpper(strings.ToLower(d.Name))
			consts = append(consts, constEntry{constName, d.Name})
		}

		w.Print("type %s int", goName)
		w.Print("const (")
		for _, c := range consts {
			w.Print("%s %s = C.%s", c.goName, goName, c.cName)
		}
		w.Print(")")

		if goName != "Status" {
			w.Print("// String implements the Stringer interface, which is used in places like fmt's %%q.  For all enums like this it returns the Go name of the constant.")
			w.Print("func (i %s) String() string {", goName)
			w.Print("switch i {")
			for _, c := range consts {
				w.Print("case %s: return \"%s\"", c.goName, c.goName)
			}
			w.Print("default: return fmt.Sprintf(\"%s(%%d)\", i)", goName)
			w.Print("}")
			w.Print("}")
		}
	default:
		panic("unhandled decl " + d.String())
	}
}

func shouldBeMethod(goName string, goType string) (string, string) {
	if goType == "Context" {
		return goName, ""
	}
	for _, t := range subTypes {
		if strings.HasPrefix(goName, t.sub) && goType == t.super {
			return goName[len(t.sub):], "*" + t.sub
		}
	}
	if goType != "" && strings.HasPrefix(goName, goType) {
		return goName[len(goType):], ""
	}
	return "", ""
}

func (w *Writer) genFunc(f *cc.Decl) bool {
	name := cNameToGoUpper(f.Name)

	retType := cTypeToMap(f.Type.Base)
	if retType == nil {
		return false
	}
	var retTypeSigs []string
	var retVals []string
	if f.Type.Base.Kind == cc.Void {
		retType = nil
	} else {
		goType := retType.goType

		// If the function looks like one that returns a subtype
		// (e.g. ImageSurfaceCreate), adjust the return type code.
		for _, t := range subTypes {
			if retType.goType == "*"+t.super &&
				(strings.HasPrefix(name, t.sub) ||
					(name == "SurfaceCreateObserver" && t.sub == "SurfaceObserver")) {
				goType = "*" + t.sub
				inner := retType
				retType = &typeMap{
					cToGo: func(in string) string {
						return fmt.Sprintf("&%s{%s}", t.sub, inner.cToGo(in))
					},
					method: inner.method,
				}
				break
			}
		}
		retTypeSigs = append(retTypeSigs, goType)
	}

	outs := outParams[f.Name]
	if outs != nil {
		if len(outs) != len(f.Type.Decls) {
			panic("outParams mismatch for " + f.Name)
		}
		if retTypeSigs != nil {
			panic(f.Name + ": outParams and return type")
		}
	}
	arrayParam := -1
	if n, ok := arrayParams[f.Name]; ok {
		arrayParam = n
	}

	var inArgs []string
	var inArgTypes []string
	var callArgs []string
	var getErrorCall string
	var methodSig string
	var preCall string

	for i := 0; i < len(f.Type.Decls); i++ {
		d := f.Type.Decls[i]
		if i == 0 && d.Type.Kind == cc.Void {
			// This is a function that accepts (void).
			continue
		}

		outParam := outs != nil && outs[i]

		argName := cNameToGoLower(d.Name)
		argType := cTypeToMap(d.Type)
		if argType == nil {
			return false
		}

		methName, methType := shouldBeMethod(name, argType.method)
		if i == 0 && methName != "" {
			name = methName
			if name == "Status" {
				name = "status"
			}
			if methType == "" {
				methType = argType.goType
			}
			methodSig = fmt.Sprintf("(%s %s)", argName, methType)
			if name != "status" && methType != "Format" && methType != "SVGVersion" && methType != "*Matrix" {
				getErrorCall = fmt.Sprintf("%s.status()", argName)
			}
		} else if outParam {
			if d.Type.Kind != cc.Ptr {
				panic("non-ptr outparam")
			}
			baseType := cTypeToMap(d.Type.Base)
			argType = &typeMap{
				goType: baseType.goType,
				cToGo: func(in string) string {
					return fmt.Sprintf("%s(%s)", baseType.goType, in)
				},
				goToC: func(in string) (string, string) {
					return "&" + in, ""
				},
			}
			preCall += fmt.Sprintf("var %s C.%s\n", argName, d.Type.Base)
			retTypeSigs = append(retTypeSigs, fmt.Sprintf(argType.goType))
			retVals = append(retVals, argType.cToGo(cNameToGoLower(d.Name)))
		} else if i == arrayParam {
			baseType := cTypeToMap(d.Type.Base)
			inArgs = append(inArgs, argName)
			inArgTypes = append(inArgTypes, "[]"+baseType.goType)
			callArgs = append(callArgs, fmt.Sprintf("(*C.%s)(sliceBytes(unsafe.Pointer(&%s)))", d.Type.Base.String(), argName))
			callArgs = append(callArgs, fmt.Sprintf("C.int(len(%s))", argName))
			i++
			continue
		} else {
			inArgs = append(inArgs, argName)
			inArgTypes = append(inArgTypes, argType.goType)
		}
		if argType.goToC == nil {
			panic("in " + name + " need goToC for " + argName)
		}
		toC, varExtra := argType.goToC(argName)
		callArgs = append(callArgs, toC)
		preCall += varExtra
	}

	argSig := ""
	for i := range inArgs {
		if i > 0 {
			argSig += ", "
		}
		argSig += inArgs[i]
		if i+1 >= len(inArgTypes) || inArgTypes[i] != inArgTypes[i+1] {
			argSig += " " + inArgTypes[i]
		}
	}

	retTypeSig := strings.Join(retTypeSigs, ", ")
	if len(retTypeSigs) > 1 {
		retTypeSig = "(" + retTypeSig + ")"
	}

	w.writeDocString(f.Name, "()")
	w.Print("func %s %s(%s) %s {", methodSig, name, argSig, retTypeSig)
	if preCall != "" {
		w.Print("%s", preCall)
	}
	call := fmt.Sprintf("C.%s(%s)", f.Name, strings.Join(callArgs, ", "))

	if retType != nil {
		w.Print("ret := %s", retType.cToGo(call))
		if getErrorCall == "" && retType.method != "" {
			getErrorCall = "ret.status()"
		}
	} else {
		w.Print("%s", call)
	}

	if getErrorCall != "" {
		w.Print("if err := %s; err != nil { panic(err) }", getErrorCall)
	}

	if retTypeSigs != nil {
		if retVals != nil {
			w.Print("return %s", strings.Join(retVals, ", "))
		} else {
			w.Print("return ret")
		}
	}
	w.Print("}")
	return true
}

func (w *Writer) process(decls []*cc.Decl) {
	w.Print(`// Copyright 2015 Google Inc. All Rights Reserved.
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

// Autogenerated by gen.go, do not edit.

package cairo

import (
	"fmt"
	"io"
	"runtime"
	"unsafe"
)

/*
#cgo pkg-config: cairo
#include <cairo.h>
#if CAIRO_HAS_SVG_SURFACE
#include <cairo-svg.h>
#endif
#if CAIRO_HAS_XLIB_SURFACE
#include <cairo-xlib.h>
#endif
#include <stdlib.h>

int gocairoWriteFunc(int key, const unsigned char* data, unsigned int length);
int gocairoReadFunc(int key, const unsigned char* data, unsigned int length);

// A cairo_write_func_t for use in cairo_surface_write_to_png.
cairo_status_t gocairo_write_func(void *closure,
                                  const unsigned char *data,
                                  unsigned int length) {
  return gocairoWriteFunc(*(int*)closure, data, length)
    ? CAIRO_STATUS_SUCCESS
    : CAIRO_STATUS_WRITE_ERROR;
}

// A cairo_read_func_t for use in cairo_image_surface_create_from_png_stream.
cairo_status_t gocairo_read_func(void *closure,
                                 const unsigned char *data,
                                 unsigned int length) {
  return gocairoReadFunc(*(int*)closure, data, length)
    ? CAIRO_STATUS_SUCCESS
    : CAIRO_STATUS_WRITE_ERROR;
}
*/
import "C"

// Error implements the error interface.
func (s Status) Error() string {
	return C.GoString(C.cairo_status_to_string(C.cairo_status_t(s)))
}

// WriteToPNG encodes a Surface to an io.Writer as a PNG file.
func (surface *Surface) WriteToPNG(w io.Writer) error {
	data := writeClosure{w: w}
	key := goPointers.put(data)
	status := C.cairo_surface_write_to_png_stream((*C.cairo_surface_t)(surface.Ptr),
		(C.cairo_write_func_t)(unsafe.Pointer(C.gocairo_write_func)),
		unsafe.Pointer(&key))
	goPointers.clear(key)
	// TODO: which should we prefer between writeClosure.err and status?
	// Perhaps test against CAIRO_STATUS_WRITE_ERROR?  Needs a test case.
	return Status(status).toError()
}

// ImageSurfaceCreateFromPNGStream creates an ImageSurface from a stream of
// PNG data.
func ImageSurfaceCreateFromPNGStream(r io.Reader) (*ImageSurface, error) {
	data := readClosure{r: r}
	key := goPointers.put(data)
	surf := &ImageSurface{wrapSurface(C.cairo_image_surface_create_from_png_stream(
		(C.cairo_read_func_t)(unsafe.Pointer(C.gocairo_read_func)),
		unsafe.Pointer(&key)))}
	goPointers.clear(key)
	// TODO: which should we prefer between readClosure.err and status?
	// Perhaps test against CAIRO_STATUS_WRITE_ERROR?  Needs a test case.
	return surf, surf.status()
}

// PathIter creates an iterator over the segments within the path.
func (p *Path) Iter() *PathIter {
	return &PathIter{path: p, i: 0}
}

// PathIter iterates a Path.
type PathIter struct {
	path *Path
	i    C.int
}

// Next returns the next PathSegment, or returns nil at the end of the path.
func (pi *PathIter) Next() *PathSegment {
	if pi.i >= pi.path.Ptr.num_data {
		return nil
	}
	// path.data is an array of cairo_path_data_t, but the union makes
	// things complicated.
	dataArray := (*[1 << 30]C.cairo_path_data_t)(unsafe.Pointer(pi.path.Ptr.data))
	seg, ofs := decodePathSegment(unsafe.Pointer(&dataArray[pi.i]))
	pi.i += C.int(ofs)
	return seg
}
`)
	for _, t := range subTypes {
		w.Print(`type %s struct {
*%s
}`, t.sub, t.super)
	}

	intentionalSkips := 0
	todoSkips := 0
	for _, d := range decls {
		if reason, ok := intentionalSkip[d.Name]; ok {
			if reason != "" {
				log.Printf("skipped %s: %s", d.Name, reason)
			}
			intentionalSkips++
			continue
		}
		if reason, ok := typeTodoList[d.Name]; ok {
			log.Printf("TODO %s: %s", d.Name, reason)
			todoSkips++
			continue
		}
		if reason, ok := skipUnhandled[d.Name]; ok {
			log.Printf("TODO %s: %s", d.Name, reason)
			todoSkips++
			continue
		}

		if strings.HasSuffix(d.Name, "_func") ||
			strings.HasSuffix(d.Name, "_func_t") ||
			strings.HasSuffix(d.Name, "_callback") ||
			strings.HasSuffix(d.Name, "_callback_data") ||
			strings.HasSuffix(d.Name, "_callback_t") {
			log.Printf("TODO %s: callbacks back into Go", d.Name)
			todoSkips++
			continue
		}
		if strings.HasSuffix(d.Name, "_user_data") {
			log.Printf("skipped %s: closures mean you don't need user data(?)", d.Name)
			intentionalSkips++
			continue
		}
		if strings.HasSuffix(d.Name, "_reference") ||
			strings.HasSuffix(d.Name, "_destroy") ||
			strings.HasSuffix(d.Name, "_get_reference_count") {
			log.Printf("skipped %s: Go uses GC instead of refcounting", d.Name)
			intentionalSkips++
			continue
		}
		if d.Name == "" {
			log.Printf("skipped %s: anonymous type", d)
			intentionalSkips++
			continue
		}

		if impl, ok := manualImpl[d.Name]; ok {
			w.writeDocString(d.Name, "()")
			w.Print("%s", impl)
		} else if d.Storage == cc.Typedef {
			w.genTypeDef(d)
		} else if d.Type.Kind == cc.Func {
			if !w.genFunc(d) {
				intentionalSkips++
			}
		} else {
			log.Printf("unhandled decl: %#v", d)
			log.Printf("type %s %#v", d.Type, d.Type)
			log.Printf("type kind %s", d.Type.Kind)
			log.Printf("storage %s", d.Storage)
		}
		w.Print("")
	}
	log.Printf("%d decls total, %d skipped intentionally / %d TODO", len(decls), intentionalSkips, todoSkips)
}

func loadDevHelp(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	type Keyword struct {
		Type string `xml:"type,attr"`
		Name string `xml:"name,attr"`
		Link string `xml:"link,attr"`
	}
	type Book struct {
		Functions []*Keyword `xml:"functions>keyword"`
	}

	var book Book
	err = xml.NewDecoder(f).Decode(&book)
	if err != nil {
		return nil, err
	}
	links := map[string]string{}
	for _, f := range book.Functions {
		name := f.Name
		if strings.HasPrefix(name, "enum ") {
			name = name[5:]
		}
		if strings.HasSuffix(name, " ()") {
			name = name[:len(name)-3]
		}
		if strings.HasSuffix(name, "\xc2\xa0()") {
			name = name[:len(name)-4]
		}
		links[name] = f.Link
	}
	return links, nil
}

// checkCairoFeatures gathers the supported cairo features by checking
// pkg-config --exists.
func checkCairoFeatures(features ...string) []string {
	supported := []string{}
	for _, feature := range features {
		cmd := exec.Command("pkg-config", "--exists", feature)
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err == nil {
			supported = append(supported, feature)
		}
	}
	return supported
}

// generateInputHeader generates the header that #includes all the
// relevant cairo headers.
func generateInputHeader(headerPath string, features []string) error {
	f, err := os.Create(headerPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "/* generated by gen.go, do not edit */\n")
	fmt.Fprintf(f, "#include <cairo.h>\n")
	for _, feature := range features {
		if feature == "cairo-xlib" {
			fmt.Fprintf(f, "#include \"fake-xlib.h\"\n")
		}
		fmt.Fprintf(f, "#include <cairo/%s.h>\n", feature)
	}

	return nil
}

// generateHeader generates the header that we parse, by expanding the
// helper header using the C preprocessor.
func generateHeader(outHeaderPath string, features []string) error {
	inHeaderPath := "cairo.h"
	if err := generateInputHeader(inHeaderPath, features); err != nil {
		return err
	}
	inf, err := os.Open(inHeaderPath)
	if err != nil {
		return fmt.Errorf("open %q: %s", inHeaderPath, err)
	}
	defer inf.Close()

	outf, err := os.Create(outHeaderPath)
	if err != nil {
		return fmt.Errorf("create %q: %s", outHeaderPath, err)
	}
	defer outf.Close()

	// Gather cflags from pkg-config --cflags.
	cmd := exec.Command("pkg-config", "--cflags", "cairo")
	for _, feature := range features {
		cmd.Args = append(cmd.Args, feature)
	}
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	cflags := strings.Split(strings.TrimSpace(string(out)), " ")

	// Preprocess the cairo header.
	cmd = exec.Command("gcc", "-E")
	cmd.Args = append(cmd.Args, cflags...)
	cmd.Args = append(cmd.Args, "-")
	cmd.Stdin = inf
	cmd.Stdout = outf
	return cmd.Run()
}

func main() {
	// features is a map from pkg-config name to whether the cairo
	// install has that feature.  It is filled in by probing
	// pkg-config.
	features := checkCairoFeatures("cairo-svg", "cairo-xlib")
	log.Printf("cairo features: %v", features)

	headerPath := "cairo-preprocessed.h"
	generateHeader(headerPath, features)

	links, err := loadDevHelp("/usr/share/gtk-doc/html/cairo/cairo.devhelp2")
	if err != nil {
		log.Printf("%s", err)
		log.Printf("ignoring missing devhelp; generated docs will lack links to C API")
		links = map[string]string{}
	}

	f, err := os.Open(headerPath)
	if err != nil {
		log.Printf("open %q: %s", headerPath, err)
		os.Exit(1)
	}
	prog, err := cc.Read(headerPath, f)
	if err != nil {
		log.Printf("read %q: %s", headerPath, err)
		os.Exit(1)
	}

	w := &Writer{links: links}
	w.process(prog.Decls)

	_, err = os.Stdout.Write(w.Source())
	if err != nil {
		log.Printf("write: %s", err)
		os.Exit(1)
	}
}
