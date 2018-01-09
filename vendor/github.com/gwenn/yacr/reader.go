// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package yacr is yet another CSV reader (and writer) with small memory usage.
package yacr

import (
	"bufio"
	"bytes"
	"encoding"
	"fmt"
	"io"
	"reflect"
	"strconv"
)

// Reader provides an interface for reading CSV data
// (compatible with rfc4180 and extended with the option of having a separator other than ",").
// Successive calls to the Scan method will step through the 'fields', skipping the separator/newline between the fields.
// The EndOfRecord method tells when a field is terminated by a line break.
type Reader struct {
	*bufio.Scanner
	sep    byte // values separator
	quoted bool // specify if values may be quoted (when they contain separator or newline)
	guess  bool // try to guess separator based on the file header
	eor    bool // true when the most recent field has been terminated by a newline (not a separator).
	lineno int  // current line number (not record number)

	Trim    bool // trim spaces (only on unquoted values). Break rfc4180 rule: "Spaces are considered part of a field and should not be ignored."
	Comment byte // character marking the start of a line comment. When specified (not 0), line comment appears as empty line.
	Lazy    bool // specify if quoted values may contains unescaped quote not followed by a separator or a newline

	Headers map[string]int // Index (first is 1) by header
}

// DefaultReader creates a "standard" CSV reader (separator is comma and quoted mode active)
func DefaultReader(rd io.Reader) *Reader {
	return NewReader(rd, ',', true, false)
}

// NewReader returns a new CSV scanner to read from r.
// When quoted is false, values must not contain a separator or newline.
func NewReader(r io.Reader, sep byte, quoted, guess bool) *Reader {
	s := &Reader{bufio.NewScanner(r), sep, quoted, guess, true, 1, false, 0, false, nil}
	s.Split(s.ScanField)
	return s
}

// ScanHeaders loads current line as the header line.
func (s *Reader) ScanHeaders() error {
	s.Headers = make(map[string]int)
	for i := 1; s.Scan(); i++ {
		s.Headers[s.Text()] = i
		if s.EndOfRecord() {
			break
		}
	}
	return s.Err()
}

// ScanRecordByName decodes one line fields by name (name1, value1, ...).
// Specified names must match Headers.
func (s *Reader) ScanRecordByName(args ...interface{}) (int, error) {
	if len(args)%2 != 0 {
		return 0, fmt.Errorf("expected an even number of arguments: %d", len(args))
	}
	values := make([]interface{}, len(s.Headers))
	for i := 0; i < len(args); i += 2 {
		name, ok := args[i].(string)
		if !ok {
			return 0, fmt.Errorf("non-string field name at %d: %T", i, args[i])
		}
		index, ok := s.Headers[name]
		if !ok {
			return 0, fmt.Errorf("unknown field name: %s", name)
		}
		values[index-1] = args[i+1]
	}
	return s.ScanRecord(values...)
}

// ScanRecord decodes one line fields to values.
// Empty lines are ignored/skipped.
// It's like fmt.Scan or database.sql.Rows.Scan.
// Returns (0, nil) on EOF, (*, err) on error
// and (n >= 1, nil) on success (n may be less or greater than len(values)).
//   var n int
//   var err error
//   for {
//     values := make([]string, N)
//     if n, err = s.ScanRecord(&values[0]/*, &values[1], ...*/); err != nil || n == 0 {
//       break // or error handling
//     } else if (n > N) {
//       n = N // ignore extra values
//     }
//     for _, value := range values[0:n] {
//       // ...
//     }
//   }
//   if err != nil {
//     // error handling
//   }
func (s *Reader) ScanRecord(values ...interface{}) (int, error) {
	for i, value := range values {
		if !s.Scan() {
			return i, s.Err()
		}
		if i == 0 { // skip empty line (or line comment)
			for s.EndOfRecord() && len(s.Bytes()) == 0 {
				if !s.Scan() {
					return i, s.Err()
				}
			}
		}
		if err := s.value(value, true); err != nil {
			return i + 1, err
		} else if s.EndOfRecord() && i != len(values)-1 {
			return i + 1, nil
		}
	}
	if !s.EndOfRecord() {
		i := len(values)
		for ; !s.EndOfRecord(); i++ { // Consume extra fields
			if !s.Scan() {
				return i, s.Err()
			}
		}
		return i, nil
	}
	return len(values), nil
}

// ScanValue advances to the next token and decodes field's content to value.
// The value may point to data that will be overwritten by a subsequent call to Scan.
func (s *Reader) ScanValue(value interface{}) error {
	if !s.Scan() {
		return s.Err()
	}
	return s.value(value, false)
}

// Value decodes field's content to value.
// The value may point to data that will be overwritten by a subsequent call to Scan.
func (s *Reader) Value(value interface{}) error {
	return s.value(value, false)
}
func (s *Reader) value(value interface{}, copied bool) error {
	var err error
	switch value := value.(type) {
	case nil:
	case *string:
		*value = s.Text()
	case *int:
		*value, err = strconv.Atoi(s.Text())
	case *int32:
		var i int64
		i, err = strconv.ParseInt(s.Text(), 10, 32)
		*value = int32(i)
	case *int64:
		*value, err = strconv.ParseInt(s.Text(), 10, 64)
	case *bool:
		*value, err = strconv.ParseBool(s.Text())
	case *float64:
		*value, err = strconv.ParseFloat(s.Text(), 64)
	case *[]byte:
		if copied {
			v := s.Bytes()
			c := make([]byte, len(v))
			copy(c, v)
			*value = c
		} else {
			*value = s.Bytes()
		}
	case encoding.TextUnmarshaler:
		err = value.UnmarshalText(s.Bytes())
	default:
		return s.scanReflect(value)
	}
	return err
}

func (s *Reader) scanReflect(v interface{}) (err error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("unsupported type %T", v)
	}
	dv := reflect.Indirect(rv)
	switch dv.Kind() {
	case reflect.String:
		dv.SetString(s.Text())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var i int64
		i, err = strconv.ParseInt(s.Text(), 10, dv.Type().Bits())
		if err == nil {
			dv.SetInt(i)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var i uint64
		i, err = strconv.ParseUint(s.Text(), 10, dv.Type().Bits())
		if err == nil {
			dv.SetUint(i)
		}
	case reflect.Bool:
		var b bool
		b, err = strconv.ParseBool(s.Text())
		if err == nil {
			dv.SetBool(b)
		}
	case reflect.Float32, reflect.Float64:
		var f float64
		f, err = strconv.ParseFloat(s.Text(), dv.Type().Bits())
		if err == nil {
			dv.SetFloat(f)
		}
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
	return
}

// LineNumber returns current line number (not record number)
func (s *Reader) LineNumber() int {
	return s.lineno
}

// EndOfRecord returns true when the most recent field has been terminated by a newline (not a separator).
func (s *Reader) EndOfRecord() bool {
	return s.eor
}

// Sep returns the values separator used/guessed
func (s *Reader) Sep() byte {
	return s.sep
}

// SkipRecords skips n records/headers
func (s *Reader) SkipRecords(n int) error {
	i := 0
	for {
		if i == n {
			return nil
		}
		if !s.Scan() {
			return s.Err()
		}
		if s.eor {
			i++
		}
	}
}

// ScanField implements bufio.SplitFunc for CSV.
// Lexing is adapted from csv_read_one_field function in SQLite3 shell sources.
func (s *Reader) ScanField(data []byte, atEOF bool) (advance int, token []byte, err error) {
	var a int
	for {
		a, token, err = s.scanField(data, atEOF)
		advance += a
		if err != nil || a == 0 || token != nil {
			return
		}
		data = data[a:]
	}
}

func (s *Reader) scanField(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 && s.eor {
		return 0, nil, nil
	}
	if s.guess {
		s.guess = false
		if b := guess(data); b > 0 {
			s.sep = b
		}
	}
	if s.quoted && len(data) > 0 && data[0] == '"' { // quoted field (may contains separator, newline and escaped quote)
		startLineno := s.lineno
		escapedQuotes := 0
		strict := true
		var c, pc, ppc byte
		// Scan until the separator or newline following the closing quote (and ignore escaped quote)
		for i := 1; i < len(data); i++ {
			c = data[i]
			if c == '\n' {
				s.lineno++
			} else if c == '"' {
				if pc == c { // escaped quote
					pc = 0
					escapedQuotes++
					continue
				}
			}
			if pc == '"' && c == s.sep {
				s.eor = false
				return i + 1, unescapeQuotes(data[1:i-1], escapedQuotes, strict), nil
			} else if pc == '"' && c == '\n' {
				s.eor = true
				return i + 1, unescapeQuotes(data[1:i-1], escapedQuotes, strict), nil
			} else if c == '\n' && pc == '\r' && ppc == '"' {
				s.eor = true
				return i + 1, unescapeQuotes(data[1:i-2], escapedQuotes, strict), nil
			}
			if pc == '"' && c != '\r' {
				if s.Lazy {
					strict = false
				} else {
					return 0, nil, fmt.Errorf("unescaped %c character at line %d", pc, s.lineno)
				}
			}
			ppc = pc
			pc = c
		}
		if atEOF {
			if c == '"' {
				s.eor = true
				return len(data), unescapeQuotes(data[1:len(data)-1], escapedQuotes, strict), nil
			}
			// If we're at EOF, we have a non-terminated field.
			return 0, nil, fmt.Errorf("non-terminated quoted field between lines %d and %d", startLineno, s.lineno)
		}
	} else if s.eor && s.Comment != 0 && len(data) > 0 && data[0] == s.Comment { // line comment
		for i, c := range data {
			if c == '\n' {
				s.lineno++
				return i + 1, nil, nil
			}
		}
		if atEOF {
			return len(data), nil, nil
		}
	} else { // unquoted field
		// Scan until separator or newline, marking end of field.
		for i, c := range data {
			if c == s.sep {
				s.eor = false
				if s.Trim {
					return i + 1, trim(data[0:i]), nil
				}
				return i + 1, data[0:i], nil
			} else if c == '\n' {
				s.lineno++
				if i > 0 && data[i-1] == '\r' {
					s.eor = true
					if s.Trim {
						return i + 1, trim(data[0 : i-1]), nil
					}
					return i + 1, data[0 : i-1], nil
				}
				s.eor = true
				if s.Trim {
					return i + 1, trim(data[0:i]), nil
				}
				return i + 1, data[0:i], nil
			}
		}
		// If we're at EOF, we have a final field. Return it.
		if atEOF {
			s.eor = true
			if s.Trim {
				return len(data), trim(data), nil
			}
			return len(data), data, nil
		}
	}
	// Request more data.
	return 0, nil, nil
}

func unescapeQuotes(b []byte, count int, strict bool) []byte {
	if count == 0 {
		return b
	}
	for i, j := 0, 0; i < len(b); i, j = i+1, j+1 {
		b[j] = b[i]
		if b[i] == '"' && (strict || i < len(b)-1 && b[i+1] == '"') {
			i++
		}
	}
	return b[:len(b)-count]
}

func guess(data []byte) byte {
	seps := []byte{',', ';', '\t', '|', ':'}
	count := make(map[byte]uint)
	for _, b := range data {
		if bytes.IndexByte(seps, b) >= 0 {
			count[b]++
			/*} else if b == '\n' {
			break*/
		}
	}
	var max uint
	var sep byte
	for b, c := range count {
		if c > max {
			max = c
			sep = b
		}
	}
	return sep
}

// bytes.TrimSpace may return nil...
func trim(s []byte) []byte {
	t := bytes.TrimSpace(s)
	if t == nil {
		return s[0:0]
	}
	return t
}

// IsNumber determines if the current token is a number or not.
// Only works for single-byte encodings (ASCII, ISO-8859-1) and UTF-8.
func (s *Reader) IsNumber() (isNum bool, isReal bool) {
	return IsNumber(s.Bytes())
}

// Only works for single-byte encodings (ASCII, ISO-8859-1) and UTF-8.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// IsNumber determines if the string is a number or not.
// Only works for single-byte encodings (ASCII, ISO-8859-1) and UTF-8.
func IsNumber(s []byte) (isNum bool, isReal bool) {
	if len(s) == 0 {
		return false, false
	}
	i := 0
	if s[i] == '-' || s[i] == '+' { // sign
		i++
	}
	// Nor Hexadecimal nor octal supported
	digit := false
	for ; len(s) != i && isDigit(s[i]); i++ {
		digit = true
	}
	if len(s) == i { // integer "[-+]?\d*"
		return digit, false
	}
	if s[i] == '.' { // real
		for i++; len(s) != i && isDigit(s[i]); i++ { // digit(s) optional
			digit = true
		}
	}
	if len(s) == i { // real "[-+]?\d*\.\d*"
		if digit {
			return true, true
		}
		// "[-+]?\." is not a number
		return false, false
	}
	if s[i] == 'e' || s[i] == 'E' { // exponent
		i++
		if !digit || len(s) == i { // nor "[-+]?\.?e" nor "[-+]?\d*\.?\d*e" is a number
			return false, false
		}
		if s[i] == '-' || s[i] == '+' { // sign
			i++
		}
		if len(s) == i || !isDigit(s[i]) { // one digit expected
			return false, false
		}
		for i++; len(s) != i && isDigit(s[i]); i++ {
		}
	}
	if len(s) == i {
		return true, true
	}
	return false, false
}
