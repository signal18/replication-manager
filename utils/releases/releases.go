// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package releases is the one table of database release lines the server and the
// plugins share: which lines exist per flavor and which of them are long-term
// support. Built in from lts-versions.json, overridable by a newer copy in the
// plugin data directory (delivered by Cloud18 without a repman upgrade), so the
// score plugin and the rolling-upgrade targets never disagree.
package releases

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed lts-versions.json
var builtin []byte

// Table is the release table of every flavor.
type Table struct {
	Updated string              `json:"updated"`
	Comment string              `json:"comment,omitempty"`
	LTS     map[string][]string `json:"lts"`
	Lines   map[string][]string `json:"lines"`
}

// Load returns the table: the copy in dataDir (lts-versions.json) when present and
// valid, the built-in one otherwise. source says which.
func Load(dataDir string) (Table, string, error) {
	raw, source := builtin, "built-in"
	if dataDir != "" {
		f := filepath.Join(dataDir, "lts-versions.json")
		if disk, err := os.ReadFile(f); err == nil {
			raw, source = disk, f
		}
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err == nil && source != "built-in" {
		// An override older than this binary may lack a key (the "lines" list came
		// after "lts"): the built-in table fills what the override does not carry,
		// per flavor, so a partial file never empties a target.
		var b Table
		if err2 := json.Unmarshal(builtin, &b); err2 == nil {
			if t.LTS == nil {
				t.LTS = map[string][]string{}
			}
			if t.Lines == nil {
				t.Lines = map[string][]string{}
			}
			for f, v := range b.LTS {
				if len(t.LTS[f]) == 0 {
					t.LTS[f] = v
				}
			}
			for f, v := range b.Lines {
				if len(t.Lines[f]) == 0 {
					t.Lines[f] = v
				}
			}
		}
		return t, source, nil
	} else if err != nil {
		if source != "built-in" {
			// A broken override never hides the built-in table.
			t = Table{}
			if err2 := json.Unmarshal(builtin, &t); err2 == nil {
				return t, "built-in (override " + source + " unreadable: " + err.Error() + ")", nil
			}
		}
		return t, source, fmt.Errorf("bad lts-versions.json (%s): %w", source, err)
	}
	return t, source, nil
}

// Line is a release line "major.minor".
type Line struct{ Major, Minor int }

func (l Line) String() string { return strconv.Itoa(l.Major) + "." + strconv.Itoa(l.Minor) }

func (l Line) Less(o Line) bool {
	return l.Major < o.Major || (l.Major == o.Major && l.Minor < o.Minor)
}

// ParseLine reads "11.4", "11.4.6", "mariadb:11.4" or "11" (minor 0) into a Line.
func ParseLine(s string) (Line, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "/") {
		s = s[i+1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || parts[0] == "" {
		return Line{}, fmt.Errorf("not a release line: %q", s)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Line{}, fmt.Errorf("not a release line: %q", s)
	}
	minor := 0
	if len(parts) > 1 {
		if minor, err = strconv.Atoi(parts[1]); err != nil {
			return Line{}, fmt.Errorf("not a release line: %q", s)
		}
	}
	return Line{major, minor}, nil
}

// IsLTS reports whether a line is long-term support for the flavor.
func (t Table) IsLTS(flavor string, l Line) bool {
	for _, s := range t.LTS[strings.ToLower(flavor)] {
		if x, err := ParseLine(s); err == nil && x == l {
			return true
		}
	}
	return false
}

func (t Table) lines(flavor string) []Line {
	var out []Line
	for _, s := range t.Lines[strings.ToLower(flavor)] {
		if x, err := ParseLine(s); err == nil {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// Targets a rolling upgrade understands.
const (
	TargetPatch     = "patch"      // same line, latest release
	TargetNextMinor = "next-minor" // next published line of the same major
	TargetNextLTS   = "next-lts"   // next long-term line after the current one
	TargetNextMajor = "next-major" // first line of the next major
	TargetVersion   = "version"    // an explicit line or tag
)

// Resolve turns a target into the release line to move to from the current one.
// The known lines of the flavor bound next-minor and next-major, so a line that was
// never published is never asked of the registry; next-lts walks the LTS list.
func (t Table) Resolve(flavor string, current Line, target, explicit string) (Line, error) {
	flavor = strings.ToLower(flavor)
	switch strings.ToLower(strings.TrimSpace(target)) {
	case TargetPatch, "":
		return current, nil
	case TargetVersion:
		if strings.TrimSpace(explicit) == "" {
			return Line{}, fmt.Errorf("target version needs the version to move to")
		}
		return ParseLine(explicit)
	case TargetNextMinor:
		for _, l := range t.lines(flavor) {
			if l.Major == current.Major && current.Less(l) {
				return l, nil
			}
		}
		return Line{}, fmt.Errorf("no line after %s in the %s %d series is known (table lines: %v)", current, flavor, current.Major, t.Lines[flavor])
	case TargetNextLTS:
		var lts []Line
		for _, s := range t.LTS[flavor] {
			if x, err := ParseLine(s); err == nil {
				lts = append(lts, x)
			}
		}
		sort.Slice(lts, func(i, j int) bool { return lts[i].Less(lts[j]) })
		for _, l := range lts {
			if current.Less(l) {
				return l, nil
			}
		}
		return Line{}, fmt.Errorf("no long-term line after %s is known for %s (table lts: %v)", current, flavor, t.LTS[flavor])
	case TargetNextMajor:
		for _, l := range t.lines(flavor) {
			if l.Major > current.Major {
				return l, nil
			}
		}
		return Line{}, fmt.Errorf("no line of a major after %d is known for %s (table lines: %v)", current.Major, flavor, t.Lines[flavor])
	}
	return Line{}, fmt.Errorf("unknown target %q: use %s, %s, %s, %s or %s", target, TargetPatch, TargetNextMinor, TargetNextLTS, TargetNextMajor, TargetVersion)
}

// SplitImage returns the repository and tag of "repo:tag" ("" tag when absent).
func SplitImage(img string) (repo, tag string) {
	img = strings.TrimSpace(img)
	if i := strings.LastIndex(img, ":"); i > 0 && !strings.Contains(img[i:], "/") {
		return img[:i], img[i+1:]
	}
	return img, ""
}

// FlavorOfImage guesses the flavor from the repository name of an image.
func FlavorOfImage(img string) string {
	repo, _ := SplitImage(img)
	name := strings.ToLower(repo)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.Contains(name, "mariadb"):
		return "mariadb"
	case strings.Contains(name, "percona"):
		return "percona"
	case strings.Contains(name, "mysql"):
		return "mysql"
	case strings.Contains(name, "postgres"):
		return "postgres"
	}
	return name
}
