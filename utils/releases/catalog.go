// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package releases

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Catalog is the image tag list of the configurator (share/repo/repos.json, refreshed
// by the back office as plugins/data/repos.json) plus the LTS table. It is the only
// source of what releases exist: every question about a release is a lookup here,
// never a registry request (#1862).
type Catalog struct {
	Table  Table
	Tags   map[string][]Tag // by image repository: "mariadb", "mysql", "percona"
	Source string           // where the tag list came from, for the plan output
}

// Tag is one entry of the configurator list; Digest is set when the delivered list
// carries it (the back office keeps the registry answer whole).
type Tag struct {
	Name   string
	Digest string
}

// Release is one real release of a line: 11.8.9, or 8.0.46-37 for Percona.
type Release struct {
	Line  Line
	Patch int
	Build int
	Tag   string
}

var releaseRe = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9]+))?$`)
var explicitTagRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]*)?$`)

// IsExplicitTag says whether a tag names one release (11.8.9, 8.0.41-debian, a digest
// reference) rather than a moving pointer (latest, lts, 11.8, 11).
func IsExplicitTag(tag string) bool {
	return strings.Contains(tag, "@sha256:") || explicitTagRe.MatchString(tag)
}

// IsExplicitImage is IsExplicitTag on the tag of repo:tag.
func IsExplicitImage(img string) bool {
	_, tag := SplitImage(img)
	return IsExplicitTag(tag)
}

func parseRelease(tag string) (Release, bool) {
	m := releaseRe.FindStringSubmatch(tag)
	if m == nil {
		return Release{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	bld := 0
	if m[4] != "" {
		bld, _ = strconv.Atoi(m[4])
	}
	return Release{Line: Line{Major: maj, Minor: min}, Patch: pat, Build: bld, Tag: tag}, true
}

func (r Release) less(o Release) bool {
	if r.Line != o.Line {
		return r.Line.Less(o.Line)
	}
	if r.Patch != o.Patch {
		return r.Patch < o.Patch
	}
	return r.Build < o.Build
}

// Releases are the real releases of the repository, newest first.
func (c *Catalog) Releases(repo string) []Release {
	var out []Release
	for _, t := range c.Tags[repo] {
		if r, ok := parseRelease(t.Name); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[j].less(out[i]) })
	return out
}

// Lines are the lines with at least one release, newest first.
func (c *Catalog) Lines(repo string) []Line {
	var out []Line
	seen := map[Line]bool{}
	for _, r := range c.Releases(repo) {
		if !seen[r.Line] {
			seen[r.Line] = true
			out = append(out, r.Line)
		}
	}
	return out
}

func (c *Catalog) ltsLines(flavor string) []Line {
	var out []Line
	for _, s := range c.Table.LTS[strings.ToLower(flavor)] {
		if l, err := ParseLine(s); err == nil {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// GetLastMinor is the newest release of the current line (11.8 -> 11.8.9).
func (c *Catalog) GetLastMinor(repo string, current Line) (string, error) {
	for _, r := range c.Releases(repo) {
		if r.Line == current {
			return r.Tag, nil
		}
	}
	return "", fmt.Errorf("no release of %s %s in the image list (%s)", repo, current, c.Source)
}

// GetNextMinor is the newest release of the next line of the same major (11.4 -> 11.5.x).
func (c *Catalog) GetNextMinor(repo string, current Line) (string, error) {
	var next *Line
	for _, l := range c.Lines(repo) {
		if l.Major == current.Major && current.Less(l) && (next == nil || l.Less(*next)) {
			x := l
			next = &x
		}
	}
	if next == nil {
		return "", fmt.Errorf("no line after %s in the %s %d series in the image list (%s)", current, repo, current.Major, c.Source)
	}
	return c.GetLastMinor(repo, *next)
}

// GetNextMajor is the newest release of the first line of the next major (11.x -> 12.0.x).
func (c *Catalog) GetNextMajor(repo string, current Line) (string, error) {
	var next *Line
	for _, l := range c.Lines(repo) {
		if l.Major > current.Major && (next == nil || l.Less(*next)) {
			x := l
			next = &x
		}
	}
	if next == nil {
		return "", fmt.Errorf("no line of a major after %d for %s in the image list (%s)", current.Major, repo, c.Source)
	}
	return c.GetLastMinor(repo, *next)
}

// GetNextMajorLTS is the newest release of the next long-term line after the current one.
func (c *Catalog) GetNextMajorLTS(repo, flavor string, current Line) (string, error) {
	for _, l := range c.ltsLines(flavor) {
		if current.Less(l) {
			if tag, err := c.GetLastMinor(repo, l); err == nil {
				return tag, nil
			}
		}
	}
	return "", fmt.Errorf("no long-term line after %s with a release in the image list for %s (lts: %v, %s)", current, flavor, c.Table.LTS[strings.ToLower(flavor)], c.Source)
}

// GetLastMajorLTS is the newest release of the highest long-term line.
func (c *Catalog) GetLastMajorLTS(repo, flavor string) (string, error) {
	lts := c.ltsLines(flavor)
	for i := len(lts) - 1; i >= 0; i-- {
		if tag, err := c.GetLastMinor(repo, lts[i]); err == nil {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no long-term line with a release in the image list for %s (lts: %v, %s)", flavor, c.Table.LTS[strings.ToLower(flavor)], c.Source)
}

// byDigest is the release sharing the digest of a pointer tag, "" without digests.
func (c *Catalog) byDigest(repo, pointer string) string {
	digest := ""
	for _, t := range c.Tags[repo] {
		if t.Name == pointer {
			digest = t.Digest
		}
	}
	if digest == "" {
		return ""
	}
	for _, t := range c.Tags[repo] {
		if t.Digest == digest {
			if _, ok := parseRelease(t.Name); ok {
				return t.Name
			}
		}
	}
	return ""
}

// Resolve turns the declared tag into the real release the service definition carries:
// an explicit tag is itself; a line is its newest release (GetLastMinor); latest is
// the release behind its digest when the list has one, else the newest release of the
// list; lts is GetLastMajorLTS. Whatever is not found in the list comes back as the
// input, unchanged: the service definition then carries the declared name.
func (c *Catalog) Resolve(repo, flavor, tag string) string {
	tag = strings.TrimSpace(tag)
	switch {
	case tag == "" || IsExplicitTag(tag):
		return tag
	case tag == "latest":
		if r := c.byDigest(repo, tag); r != "" {
			return r
		}
		if rel := c.Releases(repo); len(rel) > 0 {
			return rel[0].Tag
		}
		return tag
	case tag == "lts":
		if r := c.byDigest(repo, tag); r != "" {
			return r
		}
		if r, err := c.GetLastMajorLTS(repo, flavor); err == nil {
			return r
		}
		return tag
	}
	if l, err := ParseLine(tag); err == nil {
		if r, err := c.GetLastMinor(repo, l); err == nil {
			return r
		}
	}
	return tag
}

// Rolling upgrade targets, each a method of the list.
const (
	TargetLastLTS       = "last-lts"       // GetLastMajorLTS
	TargetLastMinor     = "last-minor"     // GetLastMinor, same as patch
	TargetPreviousMinor = "previous-minor" // GetPreviousMinor: a downgrade to the line below, same major
	TargetPreviousMajor = "previous-major" // GetPreviousMajor: a downgrade to the highest line of the previous major
)

// GetPreviousMinor is the newest release of the line just below the current one in the
// same major (11.8 -> 11.7.x): a downgrade within the major.
func (c *Catalog) GetPreviousMinor(repo string, current Line) (string, error) {
	var prev *Line
	for _, l := range c.Lines(repo) {
		if l.Major == current.Major && l.Less(current) && (prev == nil || prev.Less(l)) {
			x := l
			prev = &x
		}
	}
	if prev == nil {
		return "", fmt.Errorf("no line before %s in the %s %d series in the image list (%s)", current, repo, current.Major, c.Source)
	}
	return c.GetLastMinor(repo, *prev)
}

// GetPreviousMajor is the newest release of the highest line of the previous major
// (12.x -> 11.8.x): a downgrade across a major.
func (c *Catalog) GetPreviousMajor(repo string, current Line) (string, error) {
	var prev *Line
	for _, l := range c.Lines(repo) {
		if l.Major < current.Major && (prev == nil || prev.Less(l)) {
			x := l
			prev = &x
		}
	}
	if prev == nil {
		return "", fmt.Errorf("no line of a major before %d for %s in the image list (%s)", current.Major, repo, c.Source)
	}
	return c.GetLastMinor(repo, *prev)
}

// Target is the release a rolling upgrade moves to from the current line.
func (c *Catalog) Target(repo, flavor string, current Line, target, explicit string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case TargetPatch, TargetLastMinor, "":
		return c.GetLastMinor(repo, current)
	case TargetNextMinor:
		return c.GetNextMinor(repo, current)
	case TargetNextMajor:
		return c.GetNextMajor(repo, current)
	case TargetNextLTS:
		return c.GetNextMajorLTS(repo, flavor, current)
	case TargetLastLTS:
		return c.GetLastMajorLTS(repo, flavor)
	case TargetPreviousMinor:
		return c.GetPreviousMinor(repo, current)
	case TargetPreviousMajor:
		return c.GetPreviousMajor(repo, current)
	case TargetVersion:
		explicit = strings.TrimSpace(explicit)
		if explicit == "" {
			return "", fmt.Errorf("target version needs the version to move to")
		}
		if IsExplicitTag(explicit) {
			return explicit, nil // a given release is taken as is, in the list or not
		}
		l, err := ParseLine(explicit)
		if err != nil {
			return "", err
		}
		if r, err := c.GetLastMinor(repo, l); err == nil {
			return r, nil
		}
		return explicit, nil // a line absent from the list comes back unchanged
	}
	return "", fmt.Errorf("unknown target %q: use %s, %s, %s, %s, %s, %s, %s or %s", target, TargetPatch, TargetNextMinor, TargetNextLTS, TargetNextMajor, TargetLastLTS, TargetPreviousMinor, TargetPreviousMajor, TargetVersion)
}

// LineOf is the line of a release tag.
func LineOf(tag string) (Line, error) {
	if r, ok := parseRelease(tag); ok {
		return r.Line, nil
	}
	return ParseLine(tag)
}

// InList says whether repo:tag is an entry of the image list.
func (c *Catalog) InList(repo, tag string) bool {
	for _, t := range c.Tags[repo] {
		if t.Name == tag {
			return true
		}
	}
	return false
}

// CompareReleases orders two release tags (-1, 0, 1); a tag that is not a release
// compares equal to anything, so an unknown form never blocks a decision.
func CompareReleases(a, b string) int {
	ra, oka := parseRelease(a)
	rb, okb := parseRelease(b)
	if !oka || !okb {
		return 0
	}
	switch {
	case ra.less(rb):
		return -1
	case rb.less(ra):
		return 1
	}
	return 0
}

// HasDigest says whether the list carries the digest of a pointer tag (latest, lts): the
// delivered back-office list does, the embedded one does not, and a pointer resolved
// without it is the newest release of the list, a guess the plan says out loud.
func (c *Catalog) HasDigest(repo, pointer string) bool {
	return c.byDigest(repo, pointer) != ""
}
