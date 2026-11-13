// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package etcwriter replaces a server credential's value in the configuration files under
// /etc that replication-manager read at its start, so a rotation (a ban) does not leave the old
// password there (CREDENTIAL_STORES.md "The /etc writer"). replication-manager drops root after
// its start: a child it starts before, which stays root, does the write (Serve), asked through
// a pipe (Writer). It only replaces a key already set in a [default] section of one of the files
// it was given at its start, line by line: every other line, comment, owner and mode stays.
package etcwriter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrValue refuses a value a one-line TOML string cannot hold as is.
var ErrValue = errors.New("the value holds a line break or a control character")

// ValidValue: v can be written on one line.
func ValidValue(v string) error {
	if v == "" {
		return errors.New("empty value")
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return ErrValue
		}
	}
	return nil
}

// tomlBasic is v as a TOML basic string.
func tomlBasic(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// parseOneLineString reads a TOML basic ("...") or literal ('...') string at the start of s:
// its value and the rest of the line after it. ok false for anything else (a multi-line
// string, a number, an array): such a line is never rewritten.
func parseOneLineString(s string) (value, rest string, ok bool) {
	if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, "'''") {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(s, `"`):
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(s[i])
				}
				continue
			}
			if c == '"' {
				return b.String(), s[i+1:], true
			}
			b.WriteByte(c)
		}
	case strings.HasPrefix(s, "'"):
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return s[1 : 1+end], s[2+end:], true
		}
	}
	return "", "", false
}

// scanState follows what a line is part of: a multi-line string ("""…""" or ”'…”') or an array or
// inline table spread over several lines. A line inside one is never a header nor a key.
type scanState struct {
	multi string // the open multi-line string's delimiter, "" when none
	depth int    // open [ and { outside strings
}

// inside: the line starts inside a multi-line value.
func (st *scanState) inside() bool { return st.multi != "" || st.depth > 0 }

// feed moves the state over line (a comment ends the line, outside strings).
func (st *scanState) feed(line string) {
	for i := 0; i < len(line); i++ {
		if st.multi != "" {
			if strings.HasPrefix(line[i:], st.multi) {
				i += len(st.multi) - 1
				st.multi = ""
			} else if st.multi == `"""` && line[i] == '\\' {
				i++
			}
			continue
		}
		switch c := line[i]; {
		case strings.HasPrefix(line[i:], `"""`) || strings.HasPrefix(line[i:], "'''"):
			st.multi = line[i : i+3]
			i += 2
		case c == '"':
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' {
					i++
				}
			}
		case c == '\'':
			for i++; i < len(line) && line[i] != '\''; i++ {
			}
		case c == '#':
			return
		case c == '[' || c == '{':
			st.depth++
		case c == ']' || c == '}':
			if st.depth > 0 {
				st.depth--
			}
		}
	}
}

// header is the table a header line opens ([name], not [[name]]), ok false for any other
// line. Called only for a line outside any value (scanState).
func header(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if c := strings.IndexByte(t, '#'); c >= 0 && !strings.ContainsAny(t[:c], `"'`) {
		t = strings.TrimSpace(t[:c])
	}
	if strings.HasPrefix(t, "[[") {
		return "[[array]]", true
	}
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") || strings.ContainsAny(t[1:len(t)-1], "[]") {
		return "", false
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(t[1:len(t)-1]), `"'`)), true
}

// Replace sets key to the new value in every [default] section of content where key is set
// on one line as a string. The new value is encrypted (the "hash_..." form) where the old one
// was, else plain: the file keeps its form. found says key is set in a [default] section,
// changed that a line was rewritten (false when it already holds the value). A line holding
// key in another form (a multi-line string) is an error, never a silent skip.
func Replace(content []byte, key, plain, encrypted string) (out []byte, found, changed bool, err error) {
	lines := bytes.SplitAfter(content, []byte("\n"))
	section := ""
	var st scanState
	for i, raw := range lines {
		line := string(raw)
		if st.inside() {
			st.feed(line)
			continue
		}
		if name, ok := header(line); ok {
			section = name
			continue
		}
		st.feed(line)
		if section != "default" {
			continue
		}
		body := strings.TrimRight(line, "\r\n")
		eol := line[len(body):]
		trimmed := strings.TrimLeft(body, " \t")
		indent := body[:len(body)-len(trimmed)]
		k, after, ok := strings.Cut(trimmed, "=")
		if !ok || strings.Trim(strings.TrimSpace(k), `"'`) != key {
			continue
		}
		found = true
		old, rest, ok := parseOneLineString(strings.TrimLeft(after, " \t"))
		if !ok {
			return nil, true, false, fmt.Errorf("%s is not a one-line string in [default]: not rewritten", key)
		}
		next := plain
		if strings.HasPrefix(old, "hash_") {
			if !strings.HasPrefix(encrypted, "hash_") {
				return nil, true, false, fmt.Errorf("%s is encrypted in the file and no encrypted value was given", key)
			}
			next = encrypted
		}
		if old == next {
			continue // the same plain value (an encrypted one differs at each encryption)
		}
		lines[i] = []byte(indent + key + " = " + tomlBasic(next) + rest + eol)
		changed = true
	}
	if !changed {
		return content, found, false, nil
	}
	return bytes.Join(lines, nil), found, true, nil
}

// MaxFileSize bounds a configuration file the writer reads (F3, T18).
const MaxFileSize = 1 << 20

// tempPattern is the temporary file of a write, next to the file.
func tempPattern(path string) string { return "." + filepath.Base(path) + ".rotate-" }

// ReplaceInFile is Replace on path, read through a descriptor opened without following a
// symbolic link, then written atomically (a temporary file in the same folder, then a rename)
// with the file's mode, owner and group. found and changed as Replace; changed false leaves the
// file as it is.
func ReplaceInFile(path, key, plain, encrypted string) (found, changed bool, err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, false, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return false, false, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return false, false, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > MaxFileSize {
		f.Close()
		return false, false, fmt.Errorf("%s is larger than %d bytes", path, MaxFileSize)
	}
	content, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	f.Close()
	if err != nil {
		return false, false, err
	}
	next, found, changed, err := Replace(content, key, plain, encrypted)
	if err != nil || !changed {
		return found, false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPattern(path)+"*")
	if err != nil {
		return true, false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return true, false, err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return true, false, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
			tmp.Close()
			return true, false, err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return true, false, err
	}
	if err := tmp.Close(); err != nil {
		return true, false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return true, false, err
	}
	return true, true, nil
}

// RemoveStaleTemps removes what a write killed half-way left next to path (it holds the whole
// file, secrets included).
func RemoveStaleTemps(path string) {
	olds, _ := filepath.Glob(filepath.Join(filepath.Dir(path), tempPattern(path)+"*"))
	for _, o := range olds {
		os.Remove(o)
	}
}

// Unsafe says why uid and gid (the user replication-manager drops to) could swap path or one
// of its folders between the root writer's read and its write: path not absolute, or the file,
// a folder or a symbolic link on the way, as given or resolved, that this user owns, its group
// writes, or anyone writes (a folder only when sticky, as /tmp). "" when it is safe.
func Unsafe(path string, uid, gid int) string {
	_, why := Resolve(path, uid, gid)
	return why
}

// Resolve is path with its symbolic links resolved, and Unsafe's reason when it is not safe.
// Both the path as given (a link's own folder is on it) and the resolved one are checked.
func Resolve(path string, uid, gid int) (string, string) {
	if !filepath.IsAbs(path) {
		return "", "not an absolute path"
	}
	if why := unsafeChain(filepath.Clean(path), uid, gid); why != "" {
		return "", why
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err.Error()
	}
	if why := unsafeChain(real, uid, gid); why != "" {
		return "", why
	}
	if fi, err := os.Lstat(real); err != nil || !fi.Mode().IsRegular() {
		return "", real + " is not a regular file"
	}
	return real, ""
}

// unsafeChain checks p and each folder above it, each entry with Lstat (a link itself).
func unsafeChain(p string, uid, gid int) string {
	for ; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err.Error()
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			m := fi.Mode().Perm()
			link := fi.Mode()&os.ModeSymlink != 0
			switch {
			case int(st.Uid) == uid:
				// an owner can always give itself the write permission
				return p + " belongs to the user replication-manager runs as"
			case !link && int(st.Gid) == gid && m&0o020 != 0:
				return p + " is writable by the group replication-manager runs as"
			case !link && m&0o002 != 0 && (!fi.IsDir() || fi.Mode()&os.ModeSticky == 0):
				// a folder anyone writes is safe only sticky (/tmp): no one renames another's entry
				return p + " is writable by anyone"
			}
		}
		if p == filepath.Dir(p) {
			return ""
		}
	}
}
