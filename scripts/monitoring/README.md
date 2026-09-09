# Repman tools

A small set of read-only command line tools built on top of the
[replication-manager](https://github.com/signal18/replication-manager) (repman)
REST API: a live activity view, a topology watchdog, an event explorer, a job
explorer and a statistical ETA for reseeds and backups.

Every tool talks to the API only through `GET` requests. Nothing in this
repository can fail over, switch over, start a job or change any repman
setting.

## Tools

| Script | What it does |
| --- | --- |
| `get_top_process.py` | `top`-like live view: per cluster and per server topology, health, QPS, reads/writes per second, connections, lag, open alerts and, with `-p`, the running queries. |
| `health_check.py` | Watchdog: compares the observed role of every server with the expected one and writes a state file per cluster, meant to be picked up by a monitoring agent. |
| `get_events.py` | Merges every event source the API exposes (cluster log, task log, SQL error log, monitor log) into one filterable feed, or correlates node state changes with their surrounding events. |
| `get_jobs_logs.py` | Lists the jobs repman tracks per server (mydumper, mariabackup, xtrabackup, reseed, flashback, binlog operations, …) and their log messages. |
| `get_jobs_eta.py` | Records how long each job took, run after run, and forecasts the jobs running right now from that history, conditioned on how long they have already been running and paired with the progress their backup tooling logs. |

`lib/repman.py` holds the shared API client, the configuration loader and the
formatting helpers; the tools are standalone scripts around it.

## Requirements

* Python 3.11 or later
* A repman account allowed to read the API
* Python packages `requests`, `rich` and `pyyaml`

Each script carries its dependencies inline (PEP 723) and its shebang is
`#!/usr/bin/env -S uv run`, so with [uv](https://docs.astral.sh/uv/) installed
there is nothing to set up:

```sh
git clone <this repository>
cd <repository>
chmod +x *.py
./get_top_process.py
```

Without `uv`, install the packages yourself and call the interpreter directly:

```sh
pip install requests rich pyyaml
python3 get_top_process.py
```

## Configuration

Copy the sample file and fill in the API section, either next to the scripts
or in your user configuration directory:

```sh
cp config.yaml.default config.yaml                        # working directory

mkdir -p "$HOME/.config/repman"                           # or, per user
cp config.yaml.default "$HOME/.config/repman/config.yaml"
```

```yaml
api:
  url: "https://repman.example.net:10005"
  username: "readonly"
  password: "secret"
```

That is the whole required configuration. Clusters, their servers and the
expected master are discovered from the API on every run, so nothing has to be
declared and nothing has to be updated when the topology changes.

The file is looked up in this order:

1. `$REPMAN_CONFIG`
2. `./config.yaml`
3. `$XDG_CONFIG_HOME/repman/config.yaml`, that is
   `$HOME/.config/repman/config.yaml` when `XDG_CONFIG_HOME` is unset
4. next to the scripts

The per-user location is the practical one when the tools are shared: the
scripts stay in a common directory and every user keeps their own credentials
in `$HOME/.config/repman/config.yaml`.

The API certificate is not verified, since repman is usually published with a
self-signed certificate.

### Optional cluster overrides

`health_check.py` needs to know which server is *supposed* to be the master. It
takes the one repman flags as preferred (`db-servers-prefered-master`). When a
cluster declares none, the check falls back to the weaker invariant *exactly
one master, every other server a slave*.

To pin the expectation yourself, or to watch only part of a cluster, add an
optional `clusters` section. It overrides the discovery, cluster by cluster;
clusters not listed there keep being discovered normally.

```yaml
clusters:
  - name: "mycluster"
    master: "db1.example.net"
    hosts:
      - "db2.example.net"
      - "db3.example.net"
```

## Usage

All the tools but `health_check.py` accept `-c/--cluster NAME`, repeatable, and
default to every cluster. `--help` documents the rest.

```sh
./get_top_process.py                      # live view, all clusters
./get_top_process.py -n 2 -p              # refresh every 2s, with the queries
./get_top_process.py -o                   # a single snapshot, no live loop

./get_events.py --since 6h -L ERROR       # the errors of the last 6 hours
./get_events.py -t                        # state changes and their causes
./get_events.py -c mycluster -f backup    # one cluster, messages about backups

./get_jobs_logs.py --running              # the jobs running right now
./get_jobs_logs.py --failed --since 7d    # what failed over the last week
./get_jobs_logs.py -v -L 100              # the last 100 job log messages

./get_jobs_eta.py                         # the jobs running now, with their ETA
./get_jobs_eta.py --stats                 # the duration statistics instead
./get_jobs_eta.py -t reseed --by-server   # reseed durations, per server
./get_jobs_eta.py --json                  # machine readable output

./health_check.py                         # watchdog, refreshes every 30s
```

### health_check.py

The script keeps a live table on screen and writes one state file per cluster:

```
/var/tmp/repman-state-<cluster>
```

It contains `OK`, `Recent change. Skipping.` while a transition is still
settling, or the reason of the failure, for example:

```
db1.example.net is Slave. It should be 'Master'
```

A monitoring agent (Zabbix, Nagios, …) only has to read that file. Clusters
added to or removed from repman are picked up on the next refresh, without
restarting the script.

### get_jobs_eta.py

repman exposes no progress and no ETA for a reseed. It does keep its finished
jobs for a long time, but the size of a backup and the counters of a server are
only ever exposed as "right now", so this tool snapshots them on every run.

Only the running jobs are printed by default, one labelled block per job, and
`--stats` shows the duration statistics on their own. Use `--history PATH` to move the
recorded files elsewhere, and `--prune DAYS` to drop the old samples. A forecast
is only as good as those snapshots are frequent, so run it from cron:

```
*/5 * * * * /path/to/get_jobs_eta.py -q
```

A cluster is reseeded twice a year and its dataset grows the whole time, so
past reseed durations forecast almost nothing: 4h22m on 40 GB says nothing
about the same job on 70 GB. A running job is therefore forecast three ways,
and the `Based on` column names the one in use:

| basis | how | needs |
|---|---|---|
| `chunks` | the chunks left, each costed at its own table's median, / threads | timed chunks in the job log |
| `measured` | `(total - done) / speed` | two counter samples of the target |
| `size` | dump time of the backup being restored, x the restore factor | a recorded backup |
| `history` | past durations, conditioned on the elapsed time | 3 past runs |

`chunks` is the one that matters on a logical restore, because the remaining
work is not like the work already done: on one production cluster the chunks of
`tb_accounts` took a median of 6h51m each while those of `backup_api` tables
took a second, and that single table was 76% of the job. myloader does not
restore table by table — its threads pull chunks from a shared queue — but each
thread is sequential, so the gap between two of its lines is the chunk it
announced first. That is what makes a per-table median measurable, and the
medians accumulate in `chunk_history.jsonl` across reseeds of the same cluster.

`total` is the live size of the master, data plus indexes, from its schema.
`done` and `speed` come from the InnoDB pages the target has created — repman
collects table sizes from the master only, so a slave being rebuilt cannot be
measured any other way. Two samples are needed before a speed exists, so the
measured ETA appears one run after the job starts. When the target was not
restarted with the job its counter also holds what came before, and the row
says so.

The restore factor is how much longer a restore takes than the dump it
restores (8.5 to start with). Unlike a duration that ratio survives
the dataset growing, so it is recalibrated automatically from every reseed
recorded together with its source backup.

The history is kept as the fallback, and it is conditioned on the elapsed time:
only past runs still going at the same point forecast the end, so the ETA moves
forward instead of sliding into the past, and reads `beyond history` once none
reaches that far.

Last, the `Progress` column comes from the job log: the table mydumper or
myloader is working on, and how long ago it said so. A job past its ETA that
still logs chunks is slow; the same job silent for hours is stuck, and
`--quiet-after` (default `1h`) is only the floor: above it a job is called
stalled once it has been silent for twice the median chunk of the table it is
restoring, so a seven-hour chunk on a huge table is not mistaken for a stall. That
log is a short in-memory ring, so an empty line means "nothing recent in the
log", not "nothing happening".

Three files are kept side by side, since repman only ever exposes "right now":

```
$XDG_DATA_HOME/repman/job_history.jsonl       # finished jobs, with their source backup
$XDG_DATA_HOME/repman/backup_history.jsonl    # size and duration of each backup
$XDG_DATA_HOME/repman/restore_samples.json    # counter samples of the running jobs
$XDG_DATA_HOME/repman/chunk_history.jsonl     # per-table chunk timings
```

Its ETA covers what repman itself timed. For a physical reseed that is the
stream and the prepare, not the replication catch-up that follows: the
catch-up depends on how much the master writes meanwhile and cannot be
forecast from past runs.

## Notes and limits

* The tools are strictly read-only.
* repman keeps its logs in small in-memory ring buffers (roughly 200 lines per
  cluster source, 80 for the monitor log), so the history `get_events.py` can
  show is bounded by the daemon, not by this tool.
* `config.yaml` holds a password; keep it out of the repository and readable
  only by its owner (`chmod 600`).

## Author

Pascal Valois <pascal.valois@bso.co>

## License

This project is licensed under the GNU General Public License, version 2
(GPL-2.0): <https://www.gnu.org/licenses/old-licenses/gpl-2.0.html>.
