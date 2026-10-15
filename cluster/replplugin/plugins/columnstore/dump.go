// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The splitdump's data files hold mysqldump's INSERT statements (#2015): their values are
// read here, so the backup's rows become the first checkpoints, in LOAD DATA's format.

// dumpValue is one value of an INSERT: Null, or Text (a string unescaped, or a number as
// written), or Hex (0x... bytes), or Bits (b'...').
type dumpValue struct {
	Null bool
	Text []byte
	Hex  bool
	Bits bool
}

// forEachInsert calls fn for every row of every INSERT ... VALUES statement of r. mysqldump
// writes a statement per line (one row or many), the line ending in ";".
func forEachInsert(r io.Reader, fn func(row []dumpValue) error) error {
	br := bufio.NewReaderSize(r, 1<<20)
	var stmt bytes.Buffer
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			stmt.Write(line)
			if t := bytes.TrimRight(line, "\r\n \t"); bytes.HasSuffix(t, []byte(";")) {
				if e := parseInsert(stmt.Bytes(), fn); e != nil {
					return e
				}
				stmt.Reset()
			}
		}
		if err == io.EOF {
			if stmt.Len() > 0 {
				return parseInsert(stmt.Bytes(), fn)
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// parseInsert reads the rows of one statement; any statement other than INSERT is skipped.
func parseInsert(stmt []byte, fn func(row []dumpValue) error) error {
	s := bytes.TrimLeft(stmt, " \t\r\n")
	if !bytes.HasPrefix(bytes.ToUpper(s[:min(len(s), 6)]), []byte("INSERT")) {
		return nil
	}
	i := bytes.Index(bytes.ToUpper(s), []byte(" VALUES"))
	if i < 0 {
		return fmt.Errorf("INSERT without VALUES: %.80s", s)
	}
	p := &dumpParser{b: s, i: i + len(" VALUES")}
	for {
		p.skipSpace()
		if p.i >= len(p.b) || p.b[p.i] == ';' {
			return nil
		}
		if p.b[p.i] == ',' {
			p.i++
			continue
		}
		row, err := p.tuple()
		if err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
}

type dumpParser struct {
	b []byte
	i int
}

func (p *dumpParser) skipSpace() {
	for p.i < len(p.b) && strings.IndexByte(" \t\r\n", p.b[p.i]) >= 0 {
		p.i++
	}
}

func (p *dumpParser) tuple() ([]dumpValue, error) {
	if p.b[p.i] != '(' {
		return nil, fmt.Errorf("expected ( at %d: %.40s", p.i, p.b[p.i:])
	}
	p.i++
	var row []dumpValue
	for {
		p.skipSpace()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		row = append(row, v)
		p.skipSpace()
		if p.i >= len(p.b) {
			return nil, fmt.Errorf("unterminated row")
		}
		switch p.b[p.i] {
		case ',':
			p.i++
		case ')':
			p.i++
			return row, nil
		default:
			return nil, fmt.Errorf("unexpected %q at %d", p.b[p.i], p.i)
		}
	}
}

func (p *dumpParser) value() (dumpValue, error) {
	if p.i >= len(p.b) {
		return dumpValue{}, fmt.Errorf("unterminated row")
	}
	// _binary 'x' and the like: a charset introducer before a string
	if p.b[p.i] == '_' {
		j := p.i
		for j < len(p.b) && p.b[j] != '\'' && p.b[j] != ' ' {
			j++
		}
		p.i = j
		p.skipSpace()
	}
	switch c := p.b[p.i]; {
	case c == '\'':
		s, err := p.quoted()
		return dumpValue{Text: s}, err
	case (c == 'b' || c == 'B') && p.i+1 < len(p.b) && p.b[p.i+1] == '\'':
		p.i++
		s, err := p.quoted()
		return dumpValue{Text: s, Bits: true}, err
	case c == '0' && p.i+1 < len(p.b) && (p.b[p.i+1] == 'x' || p.b[p.i+1] == 'X'):
		j := p.i + 2
		for j < len(p.b) && isHex(p.b[j]) {
			j++
		}
		h, err := hex.DecodeString(string(p.b[p.i+2 : j]))
		p.i = j
		return dumpValue{Text: h, Hex: true}, err
	default:
		j := p.i
		for j < len(p.b) && p.b[j] != ',' && p.b[j] != ')' && strings.IndexByte(" \t\r\n", p.b[j]) < 0 {
			j++
		}
		tok := p.b[p.i:j]
		p.i = j
		if strings.EqualFold(string(tok), "NULL") {
			return dumpValue{Null: true}, nil
		}
		return dumpValue{Text: append([]byte{}, tok...)}, nil
	}
}

// quoted reads a '...' string with mysqldump's backslash escapes and ” quotes.
func (p *dumpParser) quoted() ([]byte, error) {
	p.i++
	var out []byte
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.b):
			p.i++
			switch e := p.b[p.i]; e {
			case '0':
				out = append(out, 0)
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'Z':
				out = append(out, 26)
			default:
				out = append(out, e)
			}
		case c == '\'':
			if p.i+1 < len(p.b) && p.b[p.i+1] == '\'' {
				out = append(out, '\'')
				p.i++
			} else {
				p.i++
				return out, nil
			}
		default:
			out = append(out, c)
		}
		p.i++
	}
	return nil, fmt.Errorf("unterminated string")
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// bitsToDecimal: a BIT value (b'101', or bytes) is a BIGINT UNSIGNED on ColumnStore (#2010)
func bitsToDecimal(v dumpValue) string {
	var n uint64
	if v.Bits {
		for _, c := range v.Text {
			n = n<<1 | uint64(c-'0')
		}
	} else {
		for _, c := range v.Text {
			n = n<<8 | uint64(c)
		}
	}
	return strconv.FormatUint(n, 10)
}
