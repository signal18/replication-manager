// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"fmt"
	"strconv"
	"strings"
)

// The unit ratios are SETTINGS, not constants (Stéphane 2026-09-29: "I see constants in your
// code, I don't get why this is not variables"). One source of truth: the ResourceManager's
// ratios, fed from resource-manager-ratio-dbu / -apu / -bku, exposed on every cluster's JSON
// (unitRatios) so the dashboard never types a number of its own. Format of a ratio setting:
// "cores=1,mem=4g,disk=20g,iops=1000" (keys in any order, missing key = 0 = axis excluded;
// mem in m or g, disk in g or t).
const (
	DefaultRatioDBU = "cores=1,mem=4g,disk=20g,iops=1000"
	DefaultRatioAPU = "cores=1,mem=2g,disk=10g"
	DefaultRatioBKU = "disk=20g"
)

// ParseUnitRatios reads a ratio setting. An empty string is an error so a caller keeps the
// previous value; a key it does not know is an error too.
func ParseUnitRatios(s string) (UnitRatios, error) {
	var r UnitRatios
	s = strings.TrimSpace(s)
	if s == "" {
		return r, fmt.Errorf("empty ratio")
	}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return r, fmt.Errorf("ratio %q: expected key=value", kv)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v))
		switch k {
		case "cores", "cpu":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return r, fmt.Errorf("ratio cores %q", v)
			}
			r.CoresPerUnit = f
		case "mem", "memory":
			mb, err := parseSizeMB(v)
			if err != nil {
				return r, fmt.Errorf("ratio mem %q: %w", v, err)
			}
			r.MemMBPerUnit = mb
		case "disk":
			mb, err := parseSizeMB(v)
			if err != nil {
				return r, fmt.Errorf("ratio disk %q: %w", v, err)
			}
			r.DiskGBPerUnit = mb / 1024
		case "iops", "io":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return r, fmt.Errorf("ratio iops %q", v)
			}
			r.IopsPerUnit = f
		default:
			return r, fmt.Errorf("ratio key %q unknown (cores, mem, disk, iops)", k)
		}
	}
	if r.CoresPerUnit == 0 && r.MemMBPerUnit == 0 && r.DiskGBPerUnit == 0 && r.IopsPerUnit == 0 {
		return r, fmt.Errorf("ratio %q defines no axis", s)
	}
	return r, nil
}

// parseSizeMB reads "4g", "4096m", "2t", "512" (MB) into megabytes.
func parseSizeMB(v string) (float64, error) {
	mult := 1.0
	switch {
	case strings.HasSuffix(v, "t"):
		mult, v = 1024*1024, strings.TrimSuffix(v, "t")
	case strings.HasSuffix(v, "g"):
		mult, v = 1024, strings.TrimSuffix(v, "g")
	case strings.HasSuffix(v, "m"):
		v = strings.TrimSuffix(v, "m")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("not a size")
	}
	return f * mult, nil
}

// String renders a ratio in the setting format.
func (r UnitRatios) String() string {
	parts := []string{}
	if r.CoresPerUnit > 0 {
		parts = append(parts, "cores="+strconv.FormatFloat(r.CoresPerUnit, 'f', -1, 64))
	}
	if r.MemMBPerUnit > 0 {
		parts = append(parts, "mem="+strconv.FormatFloat(r.MemMBPerUnit, 'f', -1, 64)+"m")
	}
	if r.DiskGBPerUnit > 0 {
		parts = append(parts, "disk="+strconv.FormatFloat(r.DiskGBPerUnit, 'f', -1, 64)+"g")
	}
	if r.IopsPerUnit > 0 {
		parts = append(parts, "iops="+strconv.FormatFloat(r.IopsPerUnit, 'f', -1, 64))
	}
	return strings.Join(parts, ",")
}

// mustRatio parses a built-in default; a wrong default is a programming error.
func mustRatio(s string) UnitRatios {
	r, err := ParseUnitRatios(s)
	if err != nil {
		panic("bad built-in unit ratio " + s + ": " + err.Error())
	}
	return r
}

// AllRatios is every profile's ratio, for the API and the dashboard.
func (m *ResourceManager) AllRatios() map[WorkloadProfile]UnitRatios {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[WorkloadProfile]UnitRatios, len(m.ratios))
	for k, v := range m.ratios {
		out[k] = v
	}
	return out
}

// ApplyRatioSettings feeds the three ratio settings into the manager. A setting that does
// not parse keeps the previous ratio and is returned as an error, never applied half-way.
func (m *ResourceManager) ApplyRatioSettings(dbu, apu, bku string) error {
	var errs []string
	for _, x := range []struct {
		p WorkloadProfile
		s string
	}{{ProfileDatabase, dbu}, {ProfileCompute, apu}, {ProfileStorage, bku}} {
		r, err := ParseUnitRatios(x.s)
		if err != nil {
			errs = append(errs, string(x.p)+": "+err.Error())
			continue
		}
		m.SetProfileRatios(x.p, r)
	}
	if len(errs) > 0 {
		return fmt.Errorf("unit ratio settings: %s", strings.Join(errs, "; "))
	}
	return nil
}

// computeRatioInts is the Compute (APU) ratio as whole cores / MB / GB for the app sizing
// arithmetic (app_set.go), from the manager, never a constant.
func (cluster *Cluster) computeRatioInts() (cores, memMB, diskGB int) {
	r := mustRatio(DefaultRatioAPU)
	if cluster != nil && cluster.resources != nil {
		if rr := cluster.resources.Ratios(ProfileCompute); rr.CoresPerUnit > 0 || rr.MemMBPerUnit > 0 || rr.DiskGBPerUnit > 0 {
			r = rr
		}
	}
	return int(r.CoresPerUnit + 0.5), int(r.MemMBPerUnit + 0.5), int(r.DiskGBPerUnit + 0.5)
}
