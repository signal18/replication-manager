// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yacr

import (
	"bufio"
	"encoding"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"unsafe"
)

// Writer provides an interface for writing CSV data
// (compatible with rfc4180 and extended with the option of having a separator other than ",").
// Successive calls to the Write method will automatically insert the separator.
// The EndOfRecord method tells when a line break is inserted.
type Writer struct {
	b      *bufio.Writer
	sep    byte                 // values separator
	quoted bool                 // specify if values should be quoted (when they contain a separator, a double-quote or a newline)
	sor    bool                 // true at start of record
	err    error                // sticky error.
	bs     []byte               // byte slice used to write string with minimal/no alloc/copy
	hb     *reflect.SliceHeader // header of bs

	UseCRLF bool // True to use \r\n as the line terminator
}

// DefaultWriter creates a "standard" CSV writer (separator is comma and quoted mode active)
func DefaultWriter(wr io.Writer) *Writer {
	return NewWriter(wr, ',', true)
}

// NewWriter returns a new CSV writer.
func NewWriter(w io.Writer, sep byte, quoted bool) *Writer {
	wr := &Writer{b: bufio.NewWriter(w), sep: sep, quoted: quoted, sor: true}
	wr.hb = (*reflect.SliceHeader)(unsafe.Pointer(&wr.bs))
	return wr
}

// WriteRecord ensures that values are quoted when needed.
// It's like fmt.Println.
func (w *Writer) WriteRecord(values ...interface{}) bool {
	for _, v := range values {
		if !w.WriteValue(v) {
			return false
		}
	}
	w.EndOfRecord()
	return w.err == nil
}

// WriteValue ensures that value is quoted when needed.
// Value's type/kind is used to encode value to text.
func (w *Writer) WriteValue(value interface{}) bool {
	switch value := value.(type) {
	case nil:
		return w.Write([]byte{})
	case string:
		return w.WriteString(value)
	case int:
		return w.WriteString(strconv.Itoa(value))
	case int32:
		return w.WriteString(strconv.FormatInt(int64(value), 10))
	case int64:
		return w.WriteString(strconv.FormatInt(value, 10))
	case bool:
		return w.WriteString(strconv.FormatBool(value))
	case float32:
		return w.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	case float64:
		return w.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	case []byte:
		return w.Write(value)
	case encoding.TextMarshaler: // time.Time
		if text, err := value.MarshalText(); err != nil {
			w.setErr(err)
			w.Write([]byte{}) // TODO Validate: write an empty field
			return false
		} else {
			return w.Write(text) // please, ignore golint
		}
	default:
		return w.writeReflect(value)
	}
}

// WriteReflect ensures that value is quoted when needed.
// Value's (reflect) Kind is used to encode value to text.
func (w *Writer) writeReflect(value interface{}) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		return w.WriteString(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return w.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return w.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Bool:
		return w.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Float32, reflect.Float64:
		return w.WriteString(strconv.FormatFloat(v.Float(), 'f', -1, v.Type().Bits()))
	default:
		w.setErr(fmt.Errorf("unsupported type: %T, %v", value, value))
		w.Write([]byte{}) // TODO Validate: write an empty field
		return false
	}
}

// WriteString ensures that value is quoted when needed.
func (w *Writer) WriteString(value string) bool {
	// To avoid making a copy...
	hs := (*reflect.StringHeader)(unsafe.Pointer(&value))
	w.hb.Data = hs.Data
	w.hb.Len = hs.Len
	w.hb.Cap = hs.Len
	return w.Write(w.bs)
}

var (
	// ErrNewLine is the error returned when a value contains a newline in unquoted mode.
	ErrNewLine = errors.New("yacr.Writer: newline character in value")
	// ErrSeparator is the error returned when a value contains a separator in unquoted mode.
	ErrSeparator = errors.New("yacr.Writer: separator in value")
)

// Write ensures that value is quoted when needed.
func (w *Writer) Write(value []byte) bool {
	if w.err != nil {
		return false
	}
	if !w.sor {
		w.setErr(w.b.WriteByte(w.sep))
	}
	// In quoted mode, value is enclosed between quotes if it contains sep, quote or \n.
	if w.quoted {
		last := 0
		for i, c := range value {
			switch c {
			case '"', '\r', '\n', w.sep:
			default:
				continue
			}
			if last == 0 {
				w.setErr(w.b.WriteByte('"'))
			}
			if _, err := w.b.Write(value[last : i+1]); err != nil {
				w.setErr(err)
			}
			if c == '"' {
				w.setErr(w.b.WriteByte(c)) // escaped with another double quote
			}
			last = i + 1
		}
		if _, err := w.b.Write(value[last:]); err != nil {
			w.setErr(err)
		}
		if last != 0 {
			w.setErr(w.b.WriteByte('"'))
		}
	} else {
		// check that value does not contain sep or \n
		for _, c := range value {
			switch c {
			case '\n':
				w.setErr(ErrNewLine)
				return false
			case w.sep:
				w.setErr(ErrSeparator)
				return false
			default:
				continue
			}
		}
		if _, err := w.b.Write(value); err != nil {
			w.setErr(err)
		}
	}
	w.sor = false
	return w.err == nil
}

// EndOfRecord tells when a line break must be inserted.
func (w *Writer) EndOfRecord() {
	if w.UseCRLF {
		w.setErr(w.b.WriteByte('\r'))
	}
	w.setErr(w.b.WriteByte('\n'))
	w.sor = true
}

// Flush ensures the writer's buffer is flushed.
func (w *Writer) Flush() {
	w.setErr(w.b.Flush())
}

// Err returns the first error that was encountered by the Writer.
func (w *Writer) Err() error {
	return w.err
}

// setErr records the first error encountered.
func (w *Writer) setErr(err error) {
	if w.err == nil {
		w.err = err
	}
}
