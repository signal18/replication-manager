// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A multi purge policy (#2091): the retention policy (backup-keep-*) is the default one, and
// tagged policies each take the snapshots carrying their tag (trigger:binlogs, trigger:snapback
// ...), counted apart. The cluster's apply to every archive (backup-keep-tag-policies); an
// archive that sets its own (purge-policies) replaces them, a "*" rule there replacing the
// default too.
//
// Written as rules separated by ";", each a tag then its keep values:
//
//	trigger:binlogs keep-within=7d; trigger:snapback keep-last=1

// ArchivePurgeRule is one tagged policy: the snapshots carrying Tag, kept by Policy.
type ArchivePurgeRule struct {
	Tag    string             `json:"tag"`
	Policy ArchivePurgePolicy `json:"policy"`
}

// ArchivePurgeDefaultTag is the rule of an archive's purge-policies that replaces the default.
const ArchivePurgeDefaultTag = "*"

// ArchivePurgeSettingPolicies is the archive setting holding its own policies.
const ArchivePurgeSettingPolicies = "purge-policies"

// ArchivePurgeBinlogsTag is the tag of the binlog-only snapshots: under a policy of their own,
// those taken since the last backup are always kept, whatever it keeps (KeptByPolicies).
const ArchivePurgeBinlogsTag = "trigger:binlogs"

// the keep values of a rule, as backup-keep-* without "backup-"
var purgeRuleKeys = map[string]func(p *ArchivePurgePolicy, v string) error{
	"keep-last":           func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepLast, v) },
	"keep-hourly":         func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepHourly, v) },
	"keep-daily":          func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepDaily, v) },
	"keep-weekly":         func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepWeekly, v) },
	"keep-monthly":        func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepMonthly, v) },
	"keep-yearly":         func(p *ArchivePurgePolicy, v string) error { return purgeRuleCount(&p.KeepYearly, v) },
	"keep-within":         func(p *ArchivePurgePolicy, v string) error { p.KeepWithin = v; return nil },
	"keep-within-hourly":  func(p *ArchivePurgePolicy, v string) error { p.KeepWithinHourly = v; return nil },
	"keep-within-daily":   func(p *ArchivePurgePolicy, v string) error { p.KeepWithinDaily = v; return nil },
	"keep-within-weekly":  func(p *ArchivePurgePolicy, v string) error { p.KeepWithinWeekly = v; return nil },
	"keep-within-monthly": func(p *ArchivePurgePolicy, v string) error { p.KeepWithinMonthly = v; return nil },
	"keep-within-yearly":  func(p *ArchivePurgePolicy, v string) error { p.KeepWithinYearly = v; return nil },
}

func purgeRuleCount(dst *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fmt.Errorf("not a count: %q", v)
	}
	*dst = n
	return nil
}

// ParseArchivePurgeRules reads rules written as above; "" is none. allowDefault: a "*" rule is
// accepted (an archive's own policies).
func ParseArchivePurgeRules(s string, allowDefault bool) ([]ArchivePurgeRule, error) {
	var rules []ArchivePurgeRule
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		r := ArchivePurgeRule{Tag: fields[0]}
		if strings.Contains(r.Tag, "=") {
			return nil, fmt.Errorf("purge policy %q: the tag comes first", strings.TrimSpace(part))
		}
		if r.Tag == ArchivePurgeDefaultTag && !allowDefault {
			return nil, fmt.Errorf("purge policy %q: the default policy is backup-keep-*", strings.TrimSpace(part))
		}
		if seen[r.Tag] {
			return nil, fmt.Errorf("purge policy for %s given twice", r.Tag)
		}
		seen[r.Tag] = true
		for _, kv := range fields[1:] {
			k, v, ok := strings.Cut(kv, "=")
			set, known := purgeRuleKeys[strings.ToLower(k)]
			if !ok || !known {
				return nil, fmt.Errorf("purge policy for %s: %q is not keep-<last|hourly|daily|weekly|monthly|yearly|within[-hourly...]>=<value>", r.Tag, kv)
			}
			if err := set(&r.Policy, strings.ToLower(strings.TrimSpace(v))); err != nil {
				return nil, fmt.Errorf("purge policy for %s: %s: %w", r.Tag, k, err)
			}
		}
		if err := r.Policy.Validate(); err != nil {
			return nil, fmt.Errorf("purge policy for %s: %w", r.Tag, err)
		}
		if r.Policy.IsEmpty() {
			return nil, fmt.Errorf("purge policy for %s keeps nothing: give at least one keep value", r.Tag)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// FormatArchivePurgeRules writes rules back as ParseArchivePurgeRules reads them.
func FormatArchivePurgeRules(rules []ArchivePurgeRule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, r.Tag+" "+r.Policy.keepValues())
	}
	return strings.Join(parts, "; ")
}

func (p ArchivePurgePolicy) keepValues() string {
	var out []string
	for _, kv := range []struct {
		k string
		n int
	}{{"keep-last", p.KeepLast}, {"keep-hourly", p.KeepHourly}, {"keep-daily", p.KeepDaily}, {"keep-weekly", p.KeepWeekly}, {"keep-monthly", p.KeepMonthly}, {"keep-yearly", p.KeepYearly}} {
		if kv.n > 0 {
			out = append(out, fmt.Sprintf("%s=%d", kv.k, kv.n))
		}
	}
	for _, kv := range []struct{ k, d string }{{"keep-within", p.KeepWithin}, {"keep-within-hourly", p.KeepWithinHourly}, {"keep-within-daily", p.KeepWithinDaily}, {"keep-within-weekly", p.KeepWithinWeekly}, {"keep-within-monthly", p.KeepWithinMonthly}, {"keep-within-yearly", p.KeepWithinYearly}} {
		if kv.d != "" {
			out = append(out, kv.k+"="+kv.d)
		}
	}
	return strings.Join(out, " ")
}

// ArchivePurgePolicies is what the archive a applies: its tagged policies and its default. An
// archive without purge-policies (or a nil a) applies the cluster's: backup-keep-tag-policies
// and backup-keep-*; with them, its own, and its "*" rule as default when it has one.
func (conf *Config) ArchivePurgePolicies(a *ArchiveConfig) ([]ArchivePurgeRule, ArchivePurgePolicy, error) {
	def := conf.ArchivePurgePolicy()
	if a != nil {
		if own, _ := a.Settings[ArchivePurgeSettingPolicies].(string); strings.TrimSpace(own) != "" {
			rules, err := ParseArchivePurgeRules(own, true)
			if err != nil {
				return nil, def, fmt.Errorf("archive %s: %w", a.Name, err)
			}
			tagged := rules[:0:0]
			for _, r := range rules {
				if r.Tag == ArchivePurgeDefaultTag {
					def = r.Policy
				} else {
					tagged = append(tagged, r)
				}
			}
			return tagged, def, nil
		}
	}
	rules, err := ParseArchivePurgeRules(conf.BackupKeepTagPolicies, false)
	return rules, def, err
}

// PurgeSnapshot is a snapshot as a purge sees it.
type PurgeSnapshot struct {
	ID    string
	Time  time.Time
	Tags  []string
	Group string // the snapshots counted together (restic --group-by: host and paths)
}

// KeptByPolicies is the set of snapshot ids the policies keep, the others to delete. Each
// snapshot falls under the first rule whose tag it carries, else under the default; each policy
// counts its own snapshots, by group. keepTags: a snapshot carrying one is always kept. An empty
// default keeps the snapshots it takes. The counts follow restic forget: newest first, a keep-<period>
// keeps the newest snapshot of each of the last N periods holding one, a keep-within keeps the
// snapshots within it of the newest one.
// Under the trigger:binlogs policy, the binlog-only snapshots taken since the last other
// snapshot of their group (a backup) are always kept: a binlog only extends a backup, the chain
// from the last backup is never cut, however short the policy.
func KeptByPolicies(snaps []PurgeSnapshot, rules []ArchivePurgeRule, def ArchivePurgePolicy, keepTags []string) map[string]bool {
	kept := map[string]bool{}
	classes := map[string][]PurgeSnapshot{}
	policyOf := map[string]ArchivePurgePolicy{}
	for _, s := range snaps {
		if hasAnyTag(s.Tags, keepTags) {
			kept[s.ID] = true
			continue
		}
		class, policy := "", def
		for _, r := range rules {
			if hasAnyTag(s.Tags, []string{r.Tag}) {
				class, policy = r.Tag, r.Policy
				break
			}
		}
		key := class + "\x00" + s.Group
		classes[key] = append(classes[key], s)
		policyOf[key] = policy
	}
	// the newest snapshot of each group not binlog-only: the last backup
	lastBackup := map[string]time.Time{}
	for _, s := range snaps {
		if !hasAnyTag(s.Tags, []string{ArchivePurgeBinlogsTag}) && s.Time.After(lastBackup[s.Group]) {
			lastBackup[s.Group] = s.Time
		}
	}
	for key, list := range classes {
		policy := policyOf[key]
		if class, group, _ := strings.Cut(key, "\x00"); class == ArchivePurgeBinlogsTag {
			for _, s := range list {
				if s.Time.After(lastBackup[group]) {
					kept[s.ID] = true
				}
			}
		}
		if policy.IsEmpty() {
			for _, s := range list {
				kept[s.ID] = true
			}
			continue
		}
		for id := range keptByPolicy(list, policy) {
			kept[id] = true
		}
	}
	return kept
}

func hasAnyTag(tags, want []string) bool {
	for _, w := range want {
		for _, t := range tags {
			if t == w {
				return true
			}
		}
	}
	return false
}

// keptByPolicy applies one policy to the snapshots of one group, as restic forget does.
func keptByPolicy(list []PurgeSnapshot, p ArchivePurgePolicy) map[string]bool {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	kept := map[string]bool{}
	if len(list) == 0 {
		return kept
	}
	newest := list[0].Time
	buckets := []struct {
		count  int
		within string
		period func(time.Time) string
	}{
		{p.KeepHourly, p.KeepWithinHourly, func(t time.Time) string { return t.Format("2006-01-02 15") }},
		{p.KeepDaily, p.KeepWithinDaily, func(t time.Time) string { return t.Format("2006-01-02") }},
		{p.KeepWeekly, p.KeepWithinWeekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-%02d", y, w) }},
		{p.KeepMonthly, p.KeepWithinMonthly, func(t time.Time) string { return t.Format("2006-01") }},
		{p.KeepYearly, p.KeepWithinYearly, func(t time.Time) string { return t.Format("2006") }},
	}
	for i, s := range list {
		if i < p.KeepLast {
			kept[s.ID] = true
		}
		if p.KeepWithin != "" && !s.Time.Before(resticDurationBefore(newest, p.KeepWithin)) {
			kept[s.ID] = true
		}
	}
	for _, b := range buckets {
		if b.count > 0 {
			left, last := b.count, ""
			for _, s := range list {
				if v := b.period(s.Time); v != last && left > 0 {
					kept[s.ID], last = true, v
					left--
				}
			}
		}
		if b.within != "" {
			from, last := resticDurationBefore(newest, b.within), ""
			for _, s := range list {
				if s.Time.Before(from) {
					break
				}
				if v := b.period(s.Time); v != last {
					kept[s.ID], last = true, v
				}
			}
		}
	}
	return kept
}

var resticDurationPattern = regexp.MustCompile(`^(?:(\d+)y)?(?:(\d+)m)?(?:(\d+)d)?(?:(\d+)h)?$`)

// resticDurationBefore is t minus a restic duration (2y5m7d3h).
func resticDurationBefore(t time.Time, d string) time.Time {
	m := resticDurationPattern.FindStringSubmatch(strings.ToLower(d))
	if m == nil {
		return t
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	return t.AddDate(-n(m[1]), -n(m[2]), -n(m[3])).Add(-time.Duration(n(m[4])) * time.Hour)
}
