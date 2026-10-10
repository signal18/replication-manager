#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "requests",
#     "rich",
#     "pyyaml",
# ]
# ///
"""
Repman reseed and backup ETA.

replication-manager exposes no progress and no ETA for a reseed: a job is
running, or it is not.

Past reseeds cannot fill that gap on their own. A cluster is reseeded twice
a year, its dataset grows the whole time, and a duration measured on 40 GB
says nothing about the same job on 70 GB. So this tool forecasts a running
job three ways, and shows which one it used:

  1. chunks    - what is left to restore, costed table by table at the
                 median its own chunks take, divided by the loader threads.
                 A chunk of one table can take four orders of magnitude
                 longer than a chunk of another, so this is the only model
                 that knows the remaining work is not like the work done.
  2. measured  - the bytes still to write, divided by the speed the target
                 is actually writing them at: (total - done) / speed. Blind
                 to what is coming, but it measures THIS run.
  3. size      - the dump of the backup being restored, scaled by the
                 restore factor. No measurement, but it follows the dataset
                 as it grows, which a raw duration cannot.
  4. history   - how long past runs took, conditioned on the elapsed time.
                 The last resort, and the one that ages worst.

`total` is the live size of the master, data plus indexes, from its schema.
`done` and `speed` come from the InnoDB pages the target has created since
the job started - repman collects table sizes from the master only, so a
slave being rebuilt cannot be measured any other way. Two samples are
needed before a speed exists, so a forecast appears one run after the job.

The history is still recorded, for the restore factor above and as the
fallback, and it is conditioned: only the past runs still going at the
current elapsed time are used, so the ETA moves forward instead of drifting
into the past, and says "beyond all 4 runs" once none reaches that far.

All of that is read from the mydumper / myloader lines repman relays into
its cluster task log. myloader does not restore table by table - its
threads pull chunks from a shared queue - but each thread IS sequential, so
the time between two of its lines is the chunk it announced first, and that
is what makes a per-table rate measurable. A silence is then judged against
the table being restored rather than against the clock: seven hours without
a line is a stall on a small table and business as usual on a large one.

Everything is built by snapshotting what repman only exposes as "right now"
- the last backup of each server, the counters of the target - so it is
only as complete as this tool runs often. Put it in cron:

  */5 * * * * /path/to/get_jobs_eta.py -q

  get_jobs_eta.py                       # the jobs running now, with their ETA
  get_jobs_eta.py --stats               # the duration statistics instead
  get_jobs_eta.py -t reseed -t backup   # only these task types (repeatable)
  get_jobs_eta.py --by-server           # split the statistics per server
  get_jobs_eta.py --since 90d           # only the last 90 days as samples
  get_jobs_eta.py --quiet-after 30m     # flag a job silent for 30 minutes
  get_jobs_eta.py -q                    # cron mode: record, one summary line
  get_jobs_eta.py --json                # machine readable output
  get_jobs_eta.py -c mycluster -n 30    # live view of one cluster

Caveat, before quoting any of this to a client: what is forecast is the
restore repman times. Not the replication catch-up that follows - that
depends on how much the master writes meanwhile - and not the index build
a logical restore ends with, which creates few pages while taking hours.
A job at 100% is finishing, not finished.
"""

import argparse
import json
import math
import os
import re
import statistics
import sys
import time
from datetime import datetime

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rich import box
from rich.console import Group
from rich.live import Live
from rich.table import Table

from lib.repman import (Repman, add_cluster_argument, console, epoch_str,
                        human_duration, human_size, load_config, parse_duration)

# the jobs and the job log collection already live in get_jobs_logs.py,
# next to this file
from get_jobs_logs import collect_job_logs, collect_jobs

RUNNING_STATES = ("Running", "Queued")
# a failed or cancelled job stopped early: keeping it would shorten every
# forecast, so only successful runs feed the statistics
SAMPLE_STATES = ("Success",)

# A forecast whose p90 is this many times its median describes runs too
# unlike each other to be worth quoting without a warning.
SPREAD_RATIO = 3

# mydumper and myloader report nothing to repman, they only write to the
# cluster task log, one line per table chunk, prefixed with the server the
# job runs against:
#   [db-21] ** Message: 07:10:50.962: Thread 1: restoring foo.bar part 14 of 15
HOST_RE = re.compile(r"^\[(?P<host>[^\]]+)\]\s*")
PART_RE = re.compile(r"\bpart\s+(?P<done>\d+)\s+of\s+(?P<total>\d+)", re.IGNORECASE)
# the table is quoted or bare depending on the tool and the version:
# `restoring db.table`, "dumping data for `db`.`table`"
TABLE_RE = re.compile(r"\b(?P<verb>restoring|restored|dumping|dumped)\s+"
                      r"(?:data\s+for\s+)?"
                      r"[`\"']?(?P<db>[\w$]+)[`\"']?\.[`\"']?(?P<table>[\w$]+)",
                      re.IGNORECASE)
THREAD_RE = re.compile(r"\bThread\s+(?P<n>\d+)\b", re.IGNORECASE)
# the same line, fully parsed: it is the only per-table measurement repman
# relays, and chunks of one table can take four orders of magnitude longer
# than chunks of another
CHUNK_RE = re.compile(r"Thread\s+(?P<thread>\d+):\s+restoring\s+"
                      r"[`\"']?(?P<db>[\w$]+)[`\"']?\."
                      r"[`\"']?(?P<table>[\w$]+)[`\"']?"
                      r"\s+part\s+(?P<part>\d+)\s+of\s+(?P<parts>\d+)",
                      re.IGNORECASE)
PHASES = {"restoring": "restore", "restored": "restore",
          "dumping": "dump", "dumped": "dump"}

# Go prints RFC3339 with nanoseconds, which fromisoformat refuses
ISO_RE = re.compile(r"^(?P<stamp>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})"
                    r"(?:\.(?P<frac>\d+))?(?P<zone>Z|[+-]\d{2}:?\d{2})?$")

# How much longer a restore takes than the dump of the same backup. Only a
# starting point: it is recalibrated from the reseeds this tool records.
DEFAULT_RESTORE_FACTOR = 8.5

# What the target has actually written. repman collects table sizes from the
# master only - a slave being rebuilt reports none - so the bytes that have
# landed can only be counted from the InnoDB counters of the target itself.
# Pages created is the closest thing to "new data": unlike data written it
# is not inflated by the redo log, the doublewrite buffer or page flushes.
PAGES_VAR = "INNODB_PAGES_CREATED"
PAGE_SIZE_VAR = "INNODB_PAGE_SIZE"
DEFAULT_PAGE_SIZE = 16384
# a speed measured between two cron runs is whatever chunk happened to be
# written just then, so it is averaged over at least this long
SPEED_WINDOW = 3600
# samples older than this are dropped, the baseline of a running job aside
SAMPLE_RETENTION = 6 * 3600
# when the speed of the last window and the average since the job started
# disagree by this much, projecting either one alone is a lie by omission
RATE_DIVERGENCE = 3
# a chunk is only late once it runs this much longer than its table usually
# takes. A fixed threshold cannot work: a tb_accounts chunk needs seven
# hours and a backup_api one needs a second.
STALL_FACTOR = 2


def history_path(args):
    """Where the job history is kept, $XDG_DATA_HOME/repman by default."""
    if args.history:
        path = os.path.expanduser(args.history)
    else:
        xdg = os.environ.get("XDG_DATA_HOME") or \
            os.path.join(os.path.expanduser("~"), ".local", "share")
        path = os.path.join(xdg, "repman", "job_history.jsonl")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    return path


def backup_path(path):
    """Backup history, kept next to the job history it calibrates."""
    return os.path.join(os.path.dirname(path), "backup_history.jsonl")


def samples_path(path):
    """Counter samples of the running jobs, next to the job history."""
    return os.path.join(os.path.dirname(path), "restore_samples.json")


def chunk_path(path):
    """Per-table chunk timings, next to the job history."""
    return os.path.join(os.path.dirname(path), "chunk_history.jsonl")


def iso_epoch(value):
    """RFC3339 timestamp -> epoch, or None. Naive stamps are read as UTC."""
    match = ISO_RE.match((value or "").strip())
    if not match:
        return None
    text = match.group("stamp")
    if match.group("frac"):
        text += "." + match.group("frac")[:6]
    zone = match.group("zone")
    text += "+00:00" if zone in (None, "Z") else zone
    try:
        return int(datetime.fromisoformat(text).timestamp())
    except (ValueError, OverflowError, OSError):
        return None


def task_kind(task):
    """Which backup a task dumps or restores: logical, physical, or neither."""
    name = (task or "").lower()
    if "mydumper" in name or "logical" in name:
        return "logical"
    if "backup" in name:  # mariabackup, xtrabackup
        return "physical"
    return None


def job_key(job):
    """
    Identity of a job across runs. The repman job id is a per-server
    auto-increment and is reused when the jobs table is recreated, so the
    start time is part of the key.
    """
    return "|".join(str(job.get(k) or "") for k in
                    ("cluster", "server_id", "task", "start"))


def load_history(path):
    """The recorded jobs, keyed by job_key. Unreadable lines are skipped."""
    records = {}
    if not os.path.exists(path):
        return records
    with open(path, "r") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except ValueError:
                continue
            key = record.get("key")
            if key:
                records[key] = record
    return records


def record_jobs(path, records, jobs, backups):
    """Append the finished jobs not seen yet. Returns the new ones."""
    new = []
    for job in jobs:
        if job["status"] in RUNNING_STATES:
            continue
        if not job["start"] or not job["end"] or not job["duration"]:
            continue
        key = job_key(job)
        if key in records:
            continue
        record = {
            "key": key,
            "cluster": job["cluster"],
            "server": job["server"],
            "task": job["task"],
            "status": job["status"],
            "start": job["start"],
            "end": job["end"],
            "duration": job["duration"],
            "result": job["result"][:200],
        }
        # what this job had to move, captured now: the size of a backup
        # cannot be looked up afterwards, repman only keeps the last one
        source = source_backup(backups, job["cluster"], job["start"],
                               task_kind(job["task"]))
        if source:
            record["source_id"] = source["id"]
            record["source_size"] = source["size"]
            record["source_dump"] = source["duration"]
        records[key] = record
        new.append(record)
    if new:
        with open(path, "a") as f:
            for record in new:
                f.write(json.dumps(record) + "\n")
    return new


def prune_history(path, records, days):
    """Rewrite the history without the entries older than `days`."""
    cutoff = time.time() - days * 86400
    kept = {k: r for k, r in records.items() if (r.get("start") or 0) >= cutoff}
    dropped = len(records) - len(kept)
    with open(path, "w") as f:
        for record in sorted(kept.values(), key=lambda r: r["start"]):
            f.write(json.dumps(record) + "\n")
    return kept, dropped


def collect_backups(rm, clusters):
    """
    Size and duration of the last backup of every server.

    This is what makes a forecast possible at all for a job that runs twice
    a year: a backup moves the same bytes through the same pipeline every
    week, so it measures the throughput a reseed will get, while reseeds
    themselves are far too rare to measure anything.

    repman only publishes the LAST backup of each server (lastBackupMeta),
    and its /backups endpoint returns nothing, so the series can only be
    built by snapshotting that field - one more reason to keep this tool in
    cron.
    """
    backups = []
    for name in clusters:
        for server in rm.get(f"clusters/{name}/topology/servers", quiet=True) or []:
            url = server.get("url") or ""
            for kind in ("logical", "physical"):
                meta = (server.get("lastBackupMeta") or {}).get(kind) or {}
                if not meta.get("completed") or not meta.get("size"):
                    continue
                start = iso_epoch(meta.get("startTime"))
                end = iso_epoch(meta.get("endTime"))
                if not start or not end or end <= start:
                    continue
                backups.append({
                    "key": f"{name}|{url}|{kind}|{meta.get('id')}",
                    "cluster": name,
                    "server": url,
                    "kind": kind,
                    "tool": meta.get("backupTool") or "",
                    "id": meta.get("id"),
                    "size": int(meta["size"]),
                    "start": start,
                    "end": end,
                    "duration": end - start,
                })
    return backups


def record_new(path, records, items):
    """Append the keyed records not seen yet, and return the new ones."""
    new = []
    for item in items:
        if item["key"] in records:
            continue
        records[item["key"]] = item
        new.append(item)
    if new:
        with open(path, "a") as f:
            for item in new:
                f.write(json.dumps(item) + "\n")
    return new


def cluster_totals(rm, clusters):
    """
    Logical size of each cluster: data + index, from the master schema.

    This is the total a reseed has to rebuild. It is the live size of the
    master rather than the size of the backup being restored, which is what
    makes it usable - the backup is compressed, and its own size says
    nothing about how many pages the target will end up creating.
    """
    totals = {}
    for name in clusters:
        tables = rm.get(f"clusters/{name}/schema", quiet=True) or []
        data = sum(int(t.get("data_length") or 0) for t in tables)
        index = sum(int(t.get("index_length") or 0) for t in tables)
        if data or index:
            totals[name] = {
                "count": len(tables),
                "rows": sum(int(t.get("table_rows") or 0) for t in tables),
                "bytes": data + index,
                # per table too: a chunk forecast costs what is left table
                # by table, and the small ones by their size
                "tables": {f"{t.get('table_schema')}.{t.get('table_name')}":
                           int(t.get("data_length") or 0)
                           + int(t.get("index_length") or 0)
                           for t in tables},
            }
    return totals


def collect_targets(rm, jobs):
    """
    Bytes written so far by the server behind each running job.

    One status call per server being rebuilt, and only for the ones with a
    job in flight. The counter is cumulative since the server started, not
    since the job did, so on its own it means nothing - it becomes a
    measurement once baselined against an earlier sample of itself.
    """
    targets = {}
    for job in jobs:
        key = (job["cluster"], job["server_id"])
        if job["status"] not in RUNNING_STATES or key in targets:
            continue
        status = rm.get(f"clusters/{job['cluster']}/servers/{job['server_id']}"
                        f"/status", quiet=True)
        if not isinstance(status, list):
            continue
        values = {v.get("variableName"): v.get("value")
                  for v in status if isinstance(v, dict)}
        try:
            pages = int(values.get(PAGES_VAR) or 0)
            size = int(values.get(PAGE_SIZE_VAR) or DEFAULT_PAGE_SIZE)
            uptime = int(values.get("UPTIME") or 0)
        except (TypeError, ValueError):
            continue
        if pages <= 0:
            continue
        targets[key] = {"written": pages * size, "uptime": uptime}
    return targets


def load_samples(path):
    """The recorded counter samples, keyed by job. {} when unreadable."""
    if not os.path.exists(path):
        return {}
    try:
        with open(path, "r") as f:
            return json.load(f) or {}
    except (ValueError, OSError):
        return {}


def save_samples(path, samples, live_keys, now):
    """
    Persist the samples, dropping what no longer serves a forecast: every
    job that is not running any more, and every sample too old to be part
    of a speed average - the baseline of a running job excepted, since it
    is the only thing that says how much THIS job has written.
    """
    kept = {}
    for key, series in samples.items():
        if key not in live_keys or not series:
            continue
        baseline, recent = series[0], [s for s in series[1:]
                                       if now - s["ts"] <= SAMPLE_RETENTION]
        kept[key] = [baseline] + recent
    with open(path, "w") as f:
        json.dump(kept, f)
    return kept


def sample_key(job):
    """A counter series belongs to one job, not to one server."""
    return job_key(job)


def byte_forecast(series, target, total, elapsed, now):
    """
    Remaining time as (total - done) / speed - the only forecast that does
    not need a comparable run to have happened before.

    Two things make `done` less obvious than it looks. The counter runs
    since the SERVER started, not since the job did: when repman restarted
    the target to rebuild it the two coincide and the counter is the job,
    otherwise it also holds whatever came before, and the share is then an
    upper bound rather than a measurement. And a speed taken between two
    cron runs is whatever chunk was being written just then, so it is
    averaged over the oldest sample still inside SPEED_WINDOW.

    Returns None until a second sample exists: one point measures nothing.
    """
    if not series or total <= 0:
        return None
    written = target["written"]
    # a reseed restarts the target, so an uptime no longer than the job is
    # what says the counter belongs to it. When it does not, the counter is
    # still the only measure of what has landed - it is just an upper bound,
    # since it also holds what the server wrote before the job. Counting
    # from the first sample instead would report a job 44h in as 0% done,
    # which is not a safer answer, only a wrong one.
    scoped = bool(elapsed and target["uptime"]
                  and target["uptime"] <= elapsed + SPEED_WINDOW)

    def rate(horizon):
        """Bytes per second over the samples of the last `horizon` seconds."""
        window = [s for s in series if now - s["ts"] <= horizon] or series[:1]
        span = now - window[0]["ts"]
        return (max(written - window[0]["written"], 0) / span, int(span)) \
            if span > 0 else (0, 0)

    speed, span = rate(SPEED_WINDOW)
    if not span:
        return None
    remaining = total - written
    # the pace the counter says the job has held all along. It and the
    # window above are the two rates the Speed line reports, so they are
    # the two the ETA is bracketed with - one date per rate shown.
    implied = written / elapsed if elapsed else 0
    forecast = {
        "total": total,
        "done": written,
        "since": written - series[0]["written"],
        "scoped": scoped,
        "share": min(written / total, 1.0),
        "speed": speed,
        "implied": implied,
        "eta_average": None,
        "diverges": bool(speed and implied
                         and max(speed / implied, implied / speed)
                         >= RATE_DIVERGENCE),
        "span": int(span),
        "samples": len(series),
        "remaining": None,
        "eta": None,
    }
    if remaining > 0:
        if speed > 0:
            forecast["remaining"] = int(remaining / speed)
            forecast["eta"] = int(now + remaining / speed)
        if implied > 0:
            forecast["eta_average"] = int(now + remaining / implied)
    return forecast


def chunk_durations(entries):
    """
    How long each restored chunk took, per table.

    myloader does not go table by table - its threads pull chunks from a
    shared queue and interleave them - but each thread IS sequential, so the
    time between two of its lines is the chunk it announced first. That is
    what makes a per-table rate measurable at all, and it has to be, because
    a chunk of one table can take four orders of magnitude longer than a
    chunk of another. A global bytes-per-second cannot represent that.

    The last chunk of each thread is still running and is left out.
    """
    events = []
    for entry in entries:
        host = HOST_RE.match(entry["text"])
        chunk = CHUNK_RE.search(entry["text"])
        if not host or not chunk:
            continue
        events.append({
            "cluster": entry["cluster"],
            "server": host.group("host").strip().lower(),
            "thread": int(chunk.group("thread")),
            "table": f"{chunk.group('db')}.{chunk.group('table')}",
            "part": int(chunk.group("part")),
            "parts": int(chunk.group("parts")),
            "start": int(entry["ts"].timestamp()),
        })
    events.sort(key=lambda e: e["start"])

    done, running = [], {}
    for event in events:
        thread = (event["cluster"], event["server"], event["thread"])
        previous = running.get(thread)
        if previous and event["start"] > previous["start"]:
            record = dict(previous)
            record["duration"] = event["start"] - previous["start"]
            record["key"] = (f"{record['cluster']}|{record['server']}"
                             f"|{record['thread']}|{record['start']}")
            done.append(record)
        running[thread] = event
    return done


def table_rates(records):
    """
    Median chunk duration per (cluster, table), over every job recorded.

    A table restores at the speed its rows and indexes dictate, not at the
    speed of the job it happens to be part of, so samples from previous
    reseeds of the same cluster count too.
    """
    grouped = {}
    for record in records:
        key = (record["cluster"], record["table"])
        grouped.setdefault(key, {"durations": [], "parts": 0})
        grouped[key]["durations"].append(record["duration"])
        grouped[key]["parts"] = max(grouped[key]["parts"], record["parts"])
    return {key: {"median": int(statistics.median(value["durations"])),
                  "min": min(value["durations"]),
                  "max": max(value["durations"]),
                  "parts": value["parts"],
                  "samples": len(value["durations"])}
            for key, value in grouped.items()}


def chunk_forecast(job, records, rates, sizes, elapsed, now):
    """
    Remaining time from what is left to restore, table by table.

    The work still to do is the chunks not yet seen, each costed at its own
    table's median. Tables never seen are costed by size, at the byte rate
    the measured ones imply. What the threads did before this tool started
    watching is then subtracted: without that, a job picked up mid-flight
    looks like it has done nothing.
    """
    host = job["server"].rsplit(":", 1)[0].strip().lower()
    mine = [r for r in records if r["cluster"] == job["cluster"]
            and r["server"] == host and r["start"] >= (job["start"] or 0)]
    if not mine or not sizes:
        return None
    threads = len({r["thread"] for r in mine}) or 1

    seen, parts = {}, {}
    for record in mine:
        seen.setdefault(record["table"], set()).add(record["part"])
        parts[record["table"]] = max(parts.get(record["table"], 0),
                                     record["parts"])

    def estimate(stat):
        """Thread-seconds left, costing every chunk at `stat` of its table."""
        known = done = left = covered = 0
        for table, count in parts.items():
            rate = rates.get((job["cluster"], table))
            if not rate:
                continue
            cost = rate[stat]
            known += count * cost
            done += len(seen[table]) * cost
            left += (count - len(seen[table])) * cost
            covered += sizes.get(table, 0)
        if not known or not covered:
            return None, 0
        # tables never announced yet, costed at the byte rate the measured
        # ones imply - they are the small ones, but there are a thousand
        left += max(sum(sizes.values()) - covered, 0) * known / covered
        # thread-seconds the job burned that we never observed: chunks that
        # finished before the first sample, or between two of them
        unaccounted = max(elapsed * threads - done, 0) if elapsed else 0
        return max(left - unaccounted, 0), covered

    remaining, covered = estimate("median")
    if remaining is None:
        return None
    # the same arithmetic on the best and the worst chunk each table has
    # shown: a single date hides how few chunks some of these medians rest on
    best = estimate("min")[0]
    worst = estimate("max")[0]
    return {
        "threads": threads,
        "tables": len(parts),
        "rated": sum(1 for t in parts if (job["cluster"], t) in rates),
        "chunks": len(mine),
        "coverage": covered / sum(sizes.values()),
        "remaining": int(remaining / threads),
        "eta": int(now + remaining / threads),
        "eta_best": int(now + best / threads) if best is not None else None,
        "eta_worst": int(now + worst / threads) if worst is not None else None,
    }


def source_backup(backups, cluster, before, kind):
    """
    The backup a job starting at `before` works from: the most recent one of
    the right kind that was finished by then. repman reseeds from the last
    backup it holds, so the arithmetic is just "which one was ready".
    """
    ready = [b for b in backups if b["cluster"] == cluster
             and (kind is None or b["kind"] == kind) and b["end"] <= before]
    return max(ready, key=lambda b: b["end"]) if ready else None


def restore_factors(records):
    """
    How much longer a restore takes than the dump it restores, per kind.

    A duration cannot be carried from one year to the next - the dataset
    grows underneath it - but the ratio between a restore and the dump of
    the very same backup can: it describes the pipeline, not the data. So
    that ratio is what the history is really for, and the raw hours are
    only a fallback for the jobs whose source backup is unknown.

    Returns {kind: (factor, samples)}, empty until a reseed has been
    recorded together with the backup it restored.
    """
    ratios = {}
    for record in records:
        source = record.get("source_dump")
        if not source or "reseed" not in record["task"].lower():
            continue
        ratios.setdefault(task_kind(record["task"]), []).append(
            record["duration"] / source)
    return {kind: (statistics.median(values), len(values))
            for kind, values in ratios.items()}


def percentile(values, pct):
    """Nearest-rank percentile - no interpolation, every value is a real run."""
    if not values:
        return None
    ordered = sorted(values)
    index = math.ceil(pct / 100 * len(ordered)) - 1
    return ordered[min(max(index, 0), len(ordered) - 1)]


def aggregate(records, by_server):
    """Duration statistics grouped by (cluster, task[, server])."""
    groups = {}
    for record in records:
        key = ((record["cluster"], record["task"], record["server"]) if by_server
               else (record["cluster"], record["task"]))
        groups.setdefault(key, []).append(record)

    stats = {}
    for key, group in groups.items():
        durations = sorted(r["duration"] for r in group)
        stats[key] = {
            "n": len(durations),
            "min": durations[0],
            "p50": int(statistics.median(durations)),
            "p90": percentile(durations, 90),
            "max": durations[-1],
            "last": max(r["end"] for r in group),
            # kept for conditional(): the summary alone cannot answer
            # "how did the runs that were still going at this point end"
            "durations": durations,
        }
    return stats


def conditional(durations, elapsed):
    """
    Forecast for a run that is already `elapsed` seconds old.

    A plain median predicts the past the moment a job outlives it, which is
    precisely when someone is looking at this table. Conditioning fixes
    that: a run that has lasted 44h is no longer comparable to the runs
    that were finished by then, only to the ones still going at that point.
    That is the non-parametric form of "3 of the 4 recorded runs had ended
    by now, so only the 4th one still says anything".

    The estimate only ever moves forward - the pool shrinks from the short
    end as time passes - so the ETA never walks backwards between refreshes.

    Returns (p50, p90) as total durations, or (None, None) when every
    recorded run had already finished by now: history is exhausted, and no
    honest bound can be built from it.
    """
    remaining = [d for d in durations if d > elapsed]
    if not remaining:
        return None, None
    return int(statistics.median(remaining)), percentile(remaining, 90)


def server_progress(entries, now):
    """
    What the backup tooling last said about each server, read from the
    cluster task log.

    repman publishes no progress for a job, but it does relay the output of
    the tool it drives into its per-cluster task buffer, and those lines
    carry the server in brackets, so they can be attributed without
    guessing. The buffer is a short in-memory ring (~200 lines per cluster,
    lost on a repman restart): a server missing here means "nothing recent
    in the log", never "nothing happening".

    A server running two jobs at once cannot be told apart from the log
    alone; the lines are then attributed to both.

    Returns ({(cluster, host): progress}, newest timestamp seen), the host
    lowercased and without its port.
    """
    progress, newest = {}, None
    for entry in entries:
        match = HOST_RE.match(entry["text"])
        if not match:
            continue
        ts = entry["ts"].timestamp()
        newest = ts if newest is None else max(newest, ts)
        key = (entry["cluster"], match.group("host").strip().lower())
        item = progress.setdefault(key, {
            "last": None, "work": None, "lines": 0, "threads": set(),
            "phase": None, "table": None, "part": None, "parts": None,
        })
        item["lines"] += 1
        item["last"] = ts if item["last"] is None else max(item["last"], ts)
        thread = THREAD_RE.search(entry["text"])
        if thread:
            item["threads"].add(int(thread.group("n")))

        # a chunk line is the only one that proves actual work; entries are
        # sorted oldest first, so the newest one simply overwrites
        text = entry["text"][match.end():]
        table = TABLE_RE.search(text)
        if not table or (item["work"] is not None and ts < item["work"]):
            continue
        part = PART_RE.search(text)
        item["work"] = ts
        item["phase"] = PHASES.get(table.group("verb").lower())
        item["table"] = f"{table.group('db')}.{table.group('table')}"
        item["part"] = int(part.group("done")) if part else None
        item["parts"] = int(part.group("total")) if part else None

    for item in progress.values():
        item["threads"] = len(item["threads"])
        # silence is counted from the last proven chunk, falling back to any
        # line mentioning the server when the tool logs no chunk at all
        reference = item["work"] or item["last"]
        item["quiet"] = int(now - reference) if reference else None
    return progress, newest


def forecast(job, stats_server, stats_cluster, min_samples):
    """
    ETA of a running job: the per-server history when it holds enough
    samples, the whole cluster otherwise. Returns None when neither does.
    """
    for key, scope in (((job["cluster"], job["task"], job["server"]), "server"),
                       ((job["cluster"], job["task"]), "cluster")):
        source = stats_server if scope == "server" else stats_cluster
        entry = source.get(key)
        if entry and entry["n"] >= min_samples:
            return {"scope": scope, **entry}
    return None


def rate_forecast(job, backups, factors):
    """
    ETA from what the job actually has to move, rather than from how long
    other runs of it happened to take.

    The dump of the source backup measured the same bytes through the same
    pipeline, so its duration, scaled by the restore factor, is a forecast
    that follows the dataset as it grows - which raw durations cannot do.
    Returns None when no backup of the right kind was ready by then.
    """
    kind = task_kind(job["task"])
    source = source_backup(backups, job["cluster"], job["start"], kind)
    if not source:
        return None
    factor, samples = factors.get(kind) or (DEFAULT_RESTORE_FACTOR, 0)
    return {
        "size": source["size"],
        "dump": source["duration"],
        "factor": factor,
        "calibrated": samples,
        "total": int(source["duration"] * factor),
    }


def running_rows(jobs, ctx, args):
    """
    One row per running job, forecast three ways and in this order:

      1. the bytes still to write, divided by the speed they are being
         written at - the only estimate that measures THIS run
      2. the dump of the backup it restores, scaled by the restore factor -
         no measurement, but it follows the dataset as it grows
      3. the duration of past runs, conditioned on the elapsed time - the
         last resort, and the one that ages worst

    A reseed happens twice a year, so 3 alone was never going to work.
    """
    now = ctx["now"]
    quiet_after = args.quiet_after.total_seconds() if args.quiet_after else 0
    # after a repman restart the whole buffer is old and every job looks
    # silent; only a log that is itself fresh can accuse one of being stuck
    log_fresh = bool(ctx["log_newest"] and quiet_after
                     and now - ctx["log_newest"] <= quiet_after)
    rows = []
    for job in jobs:
        if job["status"] not in RUNNING_STATES:
            continue
        elapsed = now - job["start"] if job["start"] else None
        entry = forecast(job, ctx["stats_server"], ctx["stats_cluster"],
                         args.min_samples)
        host = job["server"].rsplit(":", 1)[0].strip().lower()
        live = ctx["progress"].get((job["cluster"], host))
        target = ctx["targets"].get((job["cluster"], job["server_id"]))
        total = (ctx["totals"].get(job["cluster"]) or {}).get("bytes", 0)
        bytes_eta = None
        if target and job["start"]:
            bytes_eta = byte_forecast(ctx["samples"].get(sample_key(job)),
                                      target, total, elapsed, now)
        rate = (rate_forecast(job, ctx["backups"], ctx["factors"])
                if job["start"] else None)
        sizes = (ctx["totals"].get(job["cluster"]) or {}).get("tables") or {}
        chunk = (chunk_forecast(job, ctx["chunks"], ctx["rates"], sizes,
                                elapsed, now) if job["start"] else None)

        row = {
            "cluster": job["cluster"],
            "server": job["server"],
            "task": job["task"],
            "status": job["status"],
            "start": job["start"],
            "elapsed": elapsed,
            "eta": None,
            "basis": None,
            "eta_p50": None,
            "eta_p90": None,
            "exhausted": False,
            "exceeded": None,
            "total": total or None,
            "done": bytes_eta["done"] if bytes_eta else None,
            "since": bytes_eta["since"] if bytes_eta else None,
            "implied": bytes_eta["implied"] if bytes_eta else None,
            "eta_average": bytes_eta["eta_average"] if bytes_eta else None,
            "eta_rate": bytes_eta["eta"] if bytes_eta else None,
            "diverges": bool(bytes_eta and bytes_eta["diverges"]),
            "span": bytes_eta["span"] if bytes_eta else None,
            "scoped": bytes_eta["scoped"] if bytes_eta else None,
            "share": bytes_eta["share"] if bytes_eta else None,
            "speed": bytes_eta["speed"] if bytes_eta else None,
            "remaining": bytes_eta["remaining"] if bytes_eta else None,
            "chunks": chunk["chunks"] if chunk else 0,
            "eta_best": chunk["eta_best"] if chunk else None,
            "eta_worst": chunk["eta_worst"] if chunk else None,
            "coverage": chunk["coverage"] if chunk else None,
            "threads_used": chunk["threads"] if chunk else None,
            "expected": None,
            "source_size": rate["size"] if rate else None,
            "source_dump": rate["dump"] if rate else None,
            "factor": rate["factor"] if rate else None,
            "samples": entry["n"] if entry else 0,
            "scope": entry["scope"] if entry else None,
            "phase": live["phase"] if live else None,
            "table": live["table"] if live else None,
            "part": live["part"] if live else None,
            "parts": live["parts"] if live else None,
            "threads": live["threads"] if live else 0,
            "quiet": live["quiet"] if live else None,
            "stalled": False,
            "note": "",
        }

        notes = []
        # 3. the history, kept whatever happens: it is the only forecast
        # that can be compared against what actually happened before
        if entry and elapsed is not None:
            p50, p90 = conditional(entry["durations"], elapsed)
            if p50 is None:
                row["exhausted"] = True
                notes.append(f"beyond {entry['n']} runs "
                             f"(>{human_duration(entry['max'])})")
            else:
                row["eta_p50"] = job["start"] + p50
                row["eta_p90"] = job["start"] + p90
                if entry["p50"] and entry["p90"] >= SPREAD_RATIO * entry["p50"]:
                    notes.append("runs too unalike to forecast tightly")
        elif not entry:
            notes.append(f"no history (<{args.min_samples} runs)")

        # best basis first
        if chunk and chunk["eta"]:
            row["eta"], row["basis"] = chunk["eta"], "chunks"
            row["remaining"] = chunk["remaining"]
            notes.append(f"medians known for {chunk['rated']} of the "
                         f"{chunk['tables']} tables seen; the rest costed "
                         f"by size")
        elif bytes_eta and bytes_eta["eta"]:
            row["eta"], row["basis"] = bytes_eta["eta"], "measured"
            if not bytes_eta["scoped"]:
                notes.append("target up before the job, share is a ceiling")
            if bytes_eta["diverges"]:
                # the reader is owed the discrepancy, not one side of it
                notes.append(
                    f"pace changed: {human_size(bytes_eta['speed'])}/s now "
                    f"against {human_size(bytes_eta['implied'])}/s since the "
                    f"start, hence the range")
        elif bytes_eta and bytes_eta["share"] >= 1:
            # the last hours of a logical restore build indexes, which
            # creates almost no pages: 100% is finishing, not finished
            notes.append("all pages written, finishing (indexes, flush)")
        elif bytes_eta and not bytes_eta["speed"]:
            notes.append(f"nothing written in "
                         f"{human_duration(bytes_eta['span'])}")
        elif rate:
            row["eta"], row["basis"] = job["start"] + rate["total"], "size"
            if not rate["calibrated"]:
                basis = "uncalibrated"
            elif rate["calibrated"] < args.min_samples:
                basis = f"factor from {rate['calibrated']} run"
            else:
                basis = f"factor from {rate['calibrated']} runs"
            notes.append(f"{human_size(rate['size'])} dump "
                         f"x{rate['factor']:.1f} ({basis})")
        elif row["eta_p50"]:
            row["eta"], row["basis"] = row["eta_p50"], "history"
        elif elapsed is None:
            notes.append("not started yet")

        # an estimate the run has already outlived is not an ETA. Only a
        # measured one may sit in the past - it is a measurement, and the
        # arithmetic that produced it is still true; every other basis has
        # simply been refuted by the run, and printing its date anyway is
        # the bug this tool exists to not have.
        if row["eta"] and row["basis"] != "measured" and row["eta"] <= now:
            row["exceeded"] = int(now - row["eta"])
            if rate and rate["dump"]:
                notes.append(f"past the {row['basis']} estimate: "
                             f"{elapsed / rate['dump']:.1f}x its dump, "
                             f"not {rate['factor']:.1f}x")
            else:
                notes.append(f"past the {row['basis']} estimate by "
                             f"{human_duration(row['exceeded'])}")
            row["eta"], row["basis"] = None, None

        # silence is measured against what the table being restored usually
        # takes, not against the clock: the fixed threshold called a healthy
        # seven-hour tb_accounts chunk a stall
        if live and live["quiet"] is not None and log_fresh:
            usual = (ctx["rates"].get((job["cluster"], live["table"]))
                     if live["table"] else None)
            expected = usual["median"] * STALL_FACTOR if usual else 0
            row["expected"] = usual["median"] if usual else None
            row["stalled"] = live["quiet"] > max(quiet_after, expected)
            if row["stalled"]:
                note = f"nothing logged for {human_duration(live['quiet'])}"
                if usual:
                    note += (f", against {human_duration(usual['median'])} "
                             f"for a {live['table'].split('.')[-1]} chunk")
                notes.append(note)
        row["note"] = ", ".join(notes)
        rows.append(row)
    rows.sort(key=lambda r: (r["cluster"], -(r["start"] or 0)))
    return rows


def build_stats_table(stats, args):
    title = "Job durations - per cluster, task and server" if args.by_server \
        else "Job durations - per cluster and task"
    table = Table(box=box.MINIMAL, expand=False,
                  title=None if args.watch else title, title_style="bold green")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Task", style="bold")
    if args.by_server:
        table.add_column("Server")
    table.add_column("Runs", justify="right")
    table.add_column("Min", justify="right", style="dim")
    table.add_column("Median", justify="right", style="bold")
    table.add_column("P90", justify="right", style="yellow")
    table.add_column("Max", justify="right", style="dim")
    table.add_column("Last run")

    previous = None
    for key in sorted(stats):
        entry = stats[key]
        cluster, task = key[0], key[1]
        if previous is not None and cluster != previous:
            table.add_section()
        cells = [cluster if cluster != previous else "", task]
        previous = cluster
        if args.by_server:
            cells.append(key[2])
        # under min_samples the numbers are shown but dimmed: they are not
        # enough to forecast anything, and forecast() will ignore them
        count = (f"{entry['n']}" if entry["n"] >= args.min_samples
                 else f"[dim]{entry['n']}[/dim]")
        cells += [count,
                  human_duration(entry["min"]),
                  human_duration(entry["p50"]),
                  human_duration(entry["p90"]),
                  human_duration(entry["max"]),
                  epoch_str(entry["last"])]
        table.add_row(*cells)
    return table


def progress_cell(row):
    """What the tooling is chewing on, or '-' when the log says nothing."""
    if not row["table"]:
        return "-"
    name = (row["table"] if len(row["table"]) <= 30
            else "..." + row["table"][-27:])
    cell = f"{row['phase'] or 'work'} {name}"
    if row["parts"]:
        cell += f" {row['part']}/{row['parts']}"
    return cell


def eta_field(row):
    """
    The ETA line: a single date, an interval when the pace changed, or the
    reason there is none. Every date carries what produced it - a rate, a
    dump, a count of past runs - since a bare timestamp says nothing about
    how much it should be trusted.
    """
    if row["eta_average"] and row["eta_rate"]:
        bounds = sorted(((row["eta_rate"], row["speed"]),
                         (row["eta_average"], row["implied"])))
        return " to ".join(f"{epoch_str(when)} (at {human_size(rate)}/s)"
                           for when, rate in bounds)
    if row["eta"]:
        text = epoch_str(row["eta"])
        if row["basis"] == "chunks":
            text += (f" (from {row['chunks']} timed chunks, "
                     f"{row['threads_used']} threads, "
                     f"{row['coverage'] * 100:.0f}% of the data)")
            # the spread of the chunks each table has actually shown: with a
            # handful of samples on the table that dominates, the median
            # alone would look far more settled than it is
            if row["eta_best"] and row["eta_worst"]:
                text += (f"\nbetween {epoch_str(row['eta_best'])} and "
                         f"{epoch_str(row['eta_worst'])}")
        elif row["basis"] == "measured":
            text += f" (at {human_size(row['speed'])}/s)"
        elif row["basis"] == "size":
            text += (f" (from the {human_size(row['source_size'])} dump "
                     f"x{row['factor']:.1f})")
        elif row["basis"] == "history":
            text += f" (from {row['samples']} past runs)"
        return text
    if row["exceeded"]:
        return "unknown, past every estimate"
    if row["exhausted"]:
        return "unknown, beyond every recorded run"
    return "unknown"


def detail_fields(row):
    """The labelled lines of one running job, empty ones dropped."""
    fields = [
        ("Cluster", row["cluster"]),
        ("Server", row["server"]),
        ("Task", row["task"]),
        ("Status", row["status"]),
        ("Started", epoch_str(row["start"])),
    ]
    if row["share"] is not None:
        # the share being a ceiling is said once, in the caveats, rather
        # than as a sign glued to a byte count it does not qualify
        fields.append(("Done", f"{human_size(row['done'])}/"
                               f"{human_size(row['total'])} "
                               f"({row['share'] * 100:.0f}%)"))
    if row["speed"]:
        speed = (f"{human_size(row['speed'])}/s for the last "
                 f"{human_duration(row['span'])}")
        if row["implied"]:
            speed += (f", {human_size(row['implied'])}/s average since "
                      f"the start")
        fields.append(("Speed", speed))
    fields.append(("ETA", eta_field(row)))
    progress = progress_cell(row)
    if progress != "-":
        fields.append(("Progress", progress))
    if row["note"]:
        fields.append(("Caveats", row["note"]))
    return fields


def build_running_details(rows, args):
    """One labelled block per running job, readable without a wide terminal."""
    blocks = []
    for row in rows:
        grid = Table.grid(padding=(0, 1))
        grid.add_column(justify="left", no_wrap=True)
        grid.add_column(overflow="fold")
        for label, value in detail_fields(row):
            grid.add_row(f"{label:<9}:", value)
        blocks.append(grid)
        blocks.append("")
    return Group(*blocks[:-1]) if blocks else Group()


def snapshot(rm, clusters, args, path):
    now = int(time.time())
    jobs = collect_jobs(rm, clusters)
    if args.task:
        wanted = [t.lower() for t in args.task]
        jobs = [j for j in jobs if any(t in j["task"].lower() for t in wanted)]
    if args.server:
        jobs = [j for j in jobs if args.server.lower() in j["server"].lower()]
    running = [j for j in jobs if j["status"] in RUNNING_STATES]

    # the backups first: a job is recorded with the one it worked from, and
    # that link cannot be rebuilt afterwards
    backup_file = backup_path(path)
    backups = load_history(backup_file)
    if not args.no_record:
        record_new(backup_file, backups, collect_backups(rm, clusters))
    backups = list(backups.values())

    records = load_history(path)
    new = [] if args.no_record else record_jobs(path, records, jobs, backups)

    samples = [r for r in records.values() if r["status"] in SAMPLE_STATES]
    if args.task:
        wanted = [t.lower() for t in args.task]
        samples = [r for r in samples if any(t in r["task"].lower() for t in wanted)]
    if args.server:
        samples = [r for r in samples if args.server.lower() in r["server"].lower()]
    if clusters:
        selected = {c.lower() for c in clusters}
        samples = [r for r in samples if r["cluster"].lower() in selected]
    delta = parse_duration(args.since)
    if delta:
        cutoff = (datetime.now() - delta).timestamp()
        samples = [r for r in samples if r["start"] >= cutoff]

    # everything below only serves the running jobs, and every one of these
    # is an extra API round per cluster or per server
    chunk_file = chunk_path(path)
    targets, totals, counters, chunks = {}, {}, {}, []
    progress, log_newest = {}, None
    if running and not args.stats:
        totals = cluster_totals(rm, {j["cluster"] for j in running})
        targets = collect_targets(rm, running)
        counters = load_samples(samples_path(path))
        for job in running:
            target = targets.get((job["cluster"], job["server_id"]))
            if target:
                counters.setdefault(sample_key(job), []).append(
                    {"ts": now, "written": target["written"]})
        if not args.no_record:
            counters = save_samples(samples_path(path), counters,
                                    {sample_key(j) for j in running}, now)
        entries = collect_job_logs(rm, clusters)
        progress, log_newest = server_progress(entries, now)
        chunks = load_history(chunk_file)
        if not args.no_record:
            record_new(chunk_file, chunks, chunk_durations(entries))
        chunks = list(chunks.values())

    ctx = {
        "now": now,
        "stats_server": aggregate(samples, by_server=True),
        "stats_cluster": aggregate(samples, by_server=False),
        "backups": backups,
        "factors": restore_factors(records.values()),
        "targets": targets,
        "totals": totals,
        "samples": counters,
        "progress": progress,
        "log_newest": log_newest,
        "chunks": chunks,
        "rates": table_rates(chunks),
    }
    rows = running_rows(jobs, ctx, args)

    return {
        "new": new,
        "samples": samples,
        "stats": ctx["stats_server"] if args.by_server else ctx["stats_cluster"],
        "running": rows,
    }


def render(result, args):
    renderables = []
    # the durations are the reference material, not the answer: only the
    # running jobs print by default
    if args.stats:
        stats = result["stats"]
        renderables.append(build_stats_table(stats, args) if stats else
                           "[yellow]No job recorded yet - let this run for a "
                           "while before expecting an ETA.[/yellow]")
    if not args.stats:
        rows = result["running"]
        if rows and not args.watch:
            renderables.append("Jobs running now")
            renderables.append("")
        renderables.append(build_running_details(rows, args) if rows else
                           "No job running.")
    if args.watch:
        header = (f"Repman job ETA - {len(result['samples'])} samples - "
                  f"refresh {args.watch}s - {datetime.now():%H:%M:%S} - "
                  f"Ctrl+C to quit")
        renderables.insert(0, header)
        renderables.insert(1, "")
    return Group(*renderables)


def as_json(result, args):
    # every sample duration is in the history file already, no need to
    # repeat the whole list under each group here
    stats = [{"cluster": k[0], "task": k[1],
              **({"server": k[2]} if args.by_server else {}),
              **{f: v[f] for f in ("n", "min", "p50", "p90", "max", "last")}}
             for k, v in sorted(result["stats"].items())]
    return json.dumps({"generated": int(time.time()),
                       "samples": len(result["samples"]),
                       "stats": stats,
                       "running": result["running"]}, indent=2)


def main():
    parser = argparse.ArgumentParser(
        description="Duration history of the repman jobs, and the statistical "
                    "ETA of the ones running now.")
    add_cluster_argument(parser)
    parser.add_argument("-t", "--task", action="append", default=[], metavar="NAME",
                        help="Keep only these task types, e.g. -t reseed (repeatable).")
    parser.add_argument("--server", metavar="TEXT",
                        help="Keep only the servers matching TEXT.")
    parser.add_argument("--stats", action="store_true",
                        help="Show the job duration statistics instead of the "
                             "jobs running now.")
    parser.add_argument("--by-server", action="store_true",
                        help="Group the statistics per server instead of per cluster.")
    parser.add_argument("--since", metavar="DURATION",
                        help="Only use the runs of the last DURATION as samples, "
                             "e.g. 30d, 12h. Default: the whole history.")
    parser.add_argument("--min-samples", type=int, default=3, metavar="N",
                        help="Refuse to forecast below N past runs (default: 3).")
    parser.add_argument("--quiet-after", metavar="DURATION", default="1h",
                        help="Floor for the silence a running job is allowed, "
                             "e.g. 30m (default: 1h). Above it, the limit is "
                             "twice the median chunk of the table being "
                             "restored, so a slow table is not called a stall.")
    parser.add_argument("--history", metavar="PATH",
                        help="History file (default: "
                             "$XDG_DATA_HOME/repman/job_history.jsonl). The "
                             "backup and sample files sit next to it.")
    parser.add_argument("--no-record", action="store_true",
                        help="Report only, do not append to the history.")
    parser.add_argument("--prune", type=int, metavar="DAYS",
                        help="Drop the recorded runs older than DAYS, then exit.")
    parser.add_argument("--json", action="store_true",
                        help="Machine readable output instead of the tables.")
    parser.add_argument("-q", "--quiet", action="store_true",
                        help="Cron mode: record, print one summary line only.")
    parser.add_argument("-n", "--watch", type=int, nargs="?", const=30, default=0,
                        metavar="SECONDS",
                        help="Refresh continuously every SECONDS (default: 30).")
    parser.add_argument("--inline", action="store_true",
                        help="With -n: render in the scrollback instead of the "
                             "alternate screen.")
    args = parser.parse_args()
    args.quiet_after = parse_duration(args.quiet_after)

    path = history_path(args)

    if args.prune is not None:
        records = load_history(path)
        kept, dropped = prune_history(path, records, args.prune)
        console.print(f"Pruned {dropped} run(s) older than {args.prune}d, "
                      f"{len(kept)} kept in {path}.")
        return

    api, config_clusters = load_config()
    rm = Repman(api, config_clusters)
    clusters = rm.resolve_clusters(args.cluster)

    if not args.watch:
        result = snapshot(rm, clusters, args, path)
        if args.quiet:
            running = result["running"]
            stalled = sum(1 for r in running if r["stalled"])
            beyond = sum(1 for r in running if r["exhausted"])
            console.print(f"{datetime.now():%Y-%m-%d %H:%M:%S} "
                          f"recorded={len(result['new'])} "
                          f"history={len(result['samples'])} "
                          f"running={len(running)} "
                          f"beyond_history={beyond} stalled={stalled}")
        elif args.json:
            print(as_json(result, args))
        else:
            console.print(render(result, args))
        return

    # same Ctrl+C handling as the other repman tools: alternate screen by
    # default so the terminal is left untouched, transient inline otherwise
    alt_screen = not args.inline
    try:
        with Live(render(snapshot(rm, clusters, args, path), args),
                  console=console,
                  auto_refresh=False,
                  screen=alt_screen,
                  transient=not alt_screen,
                  vertical_overflow="crop") as live:
            while True:
                time.sleep(args.watch)
                live.update(render(snapshot(rm, clusters, args, path), args),
                            refresh=True)
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
