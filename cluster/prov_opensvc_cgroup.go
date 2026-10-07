// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import "fmt"

// openSVCServiceCgroupMount is the volume_mounts entry that gives a sensor or jobs container
// its service's cgroup (memory.current, cpu.stat, io.stat of the whole service).
//
// The slice is a systemd unit name: systemd escapes every character outside [A-Za-z0-9:_.]
// as \xHH, so a namespace or service called pg-logical lives in opensvc-ns.pg\x2dlogical.slice.
// Spelled from the {namespace}/{svcname} placeholders the path did not exist for a dashed
// name, the bind created an EMPTY cgroup there and no DBU, APU or usage was ever reported for
// those clusters (pg-logical, pg-stream, unep-prod...). The escaped path cannot be bound
// either: om3 drops the backslash on its way to docker (pgx2dlogical, checked 2026-10-06).
//
// So: a name that needs no escaping keeps the exact slice bound read-only at /svc-cgroup
// (least privilege, as before); a name that does gets the OpenSVC cgroup TREE bound read-only
// at /svc-cgroup-root, and the script finds its service's slice there by decoded name
// (dbjobs_new.sh, app_job.sh, postgres_job.sh: resolve_cgroup).
func openSVCServiceCgroupMount(namespace, svcname string) string {
	if systemdEscape(namespace) == namespace && systemdEscape(svcname) == svcname {
		return fmt.Sprintf("/sys/fs/cgroup/opensvc.slice/opensvc-ns.%s.slice/opensvc-ns.%s-svc.%s.slice:/svc-cgroup:ro", namespace, namespace, svcname)
	}
	return "/sys/fs/cgroup/opensvc.slice:/svc-cgroup-root:ro"
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
