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
| `get_jobs_eta.py` | Records how long each job took, run after run, and derives a statistical ETA (median and p90) for the jobs running right now. |

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

./get_jobs_eta.py                         # statistics, then the running ETAs
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

repman exposes no progress and no ETA for a reseed, and only keeps a short
window of finished jobs in memory. This tool appends every job that finished
since its last run to a local history file:

```
$XDG_DATA_HOME/repman/job_history.jsonl   # ~/.local/share/repman/…
```

Use `--history PATH` to move it elsewhere, and `--prune DAYS` to drop the old
samples. The forecast is only as good as the history is complete, so run it
from cron:

```
*/5 * * * * /path/to/get_jobs_eta.py -q
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
