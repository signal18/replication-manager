# On-premise jobs as a non-root user: no container paths (#2379)

## Context

With `prov-orchestrator = "onpremise"`, `onpremise-ssh = true` and `scheduler-jobs-ssh = true`, replication-manager
runs `share/scripts/dbjobs_new.sh` on the database host over SSH, as the user of `onpremise-ssh-credential`. Sites
that forbid root SSH use a regular user (in the `mysql` group, owner of `<datadir>/.system/jobs`). Such a host has
none of the container layout the script was written for, and that user cannot create a directory under `/`.

Three places assumed the container:

| Where | Before | Effect on a host |
|---|---|---|
| `print_defaults_for_fetch` | dummy configuration extracted to `/bootstrap/dummy` | `mkdir` fails, print-defaults stops, the receiver on replication-manager sees a broken pipe |
| `xtrabackup` / `mariabackup` jobs | `cd /docker-entrypoint-initdb.d` | an error line on every backup (the backup still completes: every output path is absolute) |
| `send_mariadb_defaults` | `command -v mariadbd`, then `mysqld` | not found: `/usr/sbin` is not in a regular user's PATH on Debian or RHEL |

## Decision

- The dummy configuration goes to `$TMP_DIR/dummy`. `TMP_DIR` is what replication-manager renders for
  `%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%` (`ServerMonitor.GetJobDatadir`: `<datadir>/.system/jobs`, or
  `/var/lib/replication-manager-jobs` in the legacy split layout). It exists on every orchestrator and belongs to
  the jobs user, so containers use it too: one path, no orchestrator branch. The directory is removed and
  re-created at each print-defaults, as `/bootstrap/dummy` was.
- The two physical backup jobs run from `$TMP_DIR`. Their working directory matters only for stray files a tool
  could leave there; `share/scripts/tests/mariabackup_target_dir/docker_check.sh` runs the job's command from that
  directory and checks that nothing but `backup.out` is left in it.
- `db_server_binary` is the one lookup of the server binary: `mariadbd` then `mysqld` in the PATH, then
  `/usr/sbin/mariadbd` and `/usr/sbin/mysqld`. print-defaults and the backup definitions export
  (`pr_export_definitions`, which had its own lookup with the same fallback) both use it.

The replication-manager CLI (`clients/client_config.go`) keeps `/bootstrap/dummy`: it runs in the database container
only, never over SSH on a host.

## Tests

- `share/scripts/tests/onpremise_jobs_paths/docker_check.sh`: on mariadb:10.11, mariadb:11.8 and mysql:8.4, the
  script's own print-defaults functions run as a non-root user without a passwd entry, with
  `PATH=/usr/local/bin:/usr/bin:/bin`, the API stubbed. It asserts exit 0, the binary found in `/usr/sbin`, the
  fetched variable in what is sent, the dummy under the jobs directory and nothing at `/bootstrap`, and that the
  code before the fix fails there (`cannot create directory '/bootstrap'`). It also checks that both backup jobs
  change to `$TMP_DIR`.
- `share/scripts/tests/mariabackup_target_dir/docker_check.sh`: now runs from the jobs directory. Its command
  extraction also cuts before `| job_stream_signed` (a script function absent in the container): since the stream
  signature was added the check ran a command that exited 127 with 0 bytes.
