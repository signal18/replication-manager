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

// sectionName is the table a header line opens, "" when the line is not a header. An array of
// tables ([[...]]) is never the default section.
func sectionName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	if strings.HasPrefix(t, "[[") {
		return "[[array]]", true
	}
	end := strings.IndexByte(t, ']')
	if end < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(t[1:end]), `"'`), true
}

// Replace sets key to the new value in every [default] section of content where key is set
// on one line as a string. The new value is encrypted (the "hash_..." form) where the old one
// was, else plain: the file keeps its form. changed false when key is not there, or already
// holds the value. A line holding key in another form (a multi-line string) is an error, never
// a silent skip.
func Replace(content []byte, key, plain, encrypted string) ([]byte, bool, error) {
	lines := bytes.SplitAfter(content, []byte("\n"))
	section := ""
	changed := false
	for i, raw := range lines {
		line := string(raw)
		if name, ok := sectionName(line); ok {
			section = strings.ToLower(name)
			continue
		}
		if section != "default" {
			continue
		}
		body := strings.TrimRight(line, "\r\n")
		eol := line[len(body):]
		trimmed := strings.TrimLeft(body, " \t")
		indent := body[:len(body)-len(trimmed)]
		k, after, found := strings.Cut(trimmed, "=")
		if !found || strings.Trim(strings.TrimSpace(k), `"'`) != key {
			continue
		}
		old, rest, ok := parseOneLineString(strings.TrimLeft(after, " \t"))
		if !ok {
			return nil, false, fmt.Errorf("%s is not a one-line string in [default]: not rewritten", key)
		}
		next := plain
		if strings.HasPrefix(old, "hash_") {
			if !strings.HasPrefix(encrypted, "hash_") {
				return nil, false, fmt.Errorf("%s is encrypted in the file and no encrypted value was given", key)
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
		return content, false, nil
	}
	return bytes.Join(lines, nil), true, nil
}

// ReplaceInFile is Replace on path, written atomically (a temporary file in the same folder,
// then a rename) with the file's mode, owner and group. changed false leaves the file as it is.
func ReplaceInFile(path, key, plain, encrypted string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	next, changed, err := Replace(content, key, plain, encrypted)
	if err != nil || !changed {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".rotate-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return false, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
			tmp.Close()
			return false, err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}
