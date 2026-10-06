// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import "fmt"

// openSVCServiceCgroupSlice is the host path of an OpenSVC service's cgroup slice, bound
// read-only at /svc-cgroup in the sensor and jobs containers (the DBU, APU and PostgreSQL
// usage sensors read memory.current, cpu.stat and io.stat there).
//
// The slice is a systemd unit name: systemd escapes every character outside [A-Za-z0-9:_.]
// as \xHH, so a namespace or service called pg-logical is spelled pg\x2dlogical on disk.
// The path used to be spelled from the {namespace}/{svcname} placeholders: for a dashed
// name it pointed to nothing, the bind mount created an EMPTY cgroup there, and no usage was
// ever reported for those clusters (pg-logical, pg-stream, unep-prod...). OpenSVC keeps the
// backslashes of the value as they are (checked 2026-10-06).
func openSVCServiceCgroupSlice(namespace, svcname string) string {
	ns, svc := systemdEscape(namespace), systemdEscape(svcname)
	return fmt.Sprintf("/sys/fs/cgroup/opensvc.slice/opensvc-ns.%s.slice/opensvc-ns.%s-svc.%s.slice", ns, ns, svc)
}

// openSVCServiceCgroupMount is the volume_mounts entry of that bind.
func openSVCServiceCgroupMount(namespace, svcname string) string {
	return openSVCServiceCgroupSlice(namespace, svcname) + ":/svc-cgroup:ro"
}

// systemdEscape spells a string the way systemd does in a unit name.
func systemdEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == ':', c == '_', c == '.':
			out = append(out, c)
		default:
			out = append(out, []byte(fmt.Sprintf(`\x%02x`, c))...)
		}
	}
	return string(out)
}
