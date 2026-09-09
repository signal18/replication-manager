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
Repman job duration history and statistical ETA.

replication-manager exposes no progress and no ETA for a reseed: a job is
running, or it is not. The only forecast that can honestly be given is a
statistical one, built from how long the very same job took on the very
same cluster before.

This tool keeps that history. Every run appends the jobs that finished
since the last run to a local JSONL file, then reports:

  * per cluster and task - samples, min / median / p90 / max duration
  * per job running right now - elapsed time, and the ETA derived from the
    median (likely) and from the p90 (the bound worth quoting to a client)

The repman API only keeps a short window of finished jobs in memory, so
the history is only as complete as this tool runs often. Put it in cron:

  */5 * * * * /path/to/get_jobs_eta.py -q

  get_jobs_eta.py                       # record, then stats + running ETA
  get_jobs_eta.py --running             # only the jobs running right now
  get_jobs_eta.py --stats               # only the duration statistics
  get_jobs_eta.py -t reseed -t backup   # only these task types (repeatable)
  get_jobs_eta.py --by-server           # split the statistics per server
  get_jobs_eta.py --since 90d           # only the last 90 days as samples
  get_jobs_eta.py -q                    # cron mode: record, one summary line
  get_jobs_eta.py --json                # machine readable output
  get_jobs_eta.py -c mycluster -n 30    # live view of one cluster

Caveat, before quoting any of this to a client: the duration recorded here
is what repman itself timed. For a physical reseed that covers the stream
and the prepare, NOT the replication catch-up that follows - the catch-up
depends on how much the master writes meanwhile, and is not forecastable
from past runs.
"""

import argparse
import json
import math
import os
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
                        human_duration, load_config, parse_duration)

# the jobs collection already lives in get_jobs_logs.py, next to this file
from get_jobs_logs import collect_jobs

RUNNING_STATES = ("Running", "Queued")
# a failed or cancelled job stopped early: keeping it would shorten every
# forecast, so only successful runs feed the statistics
SAMPLE_STATES = ("Success",)


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


def record_jobs(path, records, jobs):
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
        durations = [r["duration"] for r in group]
        stats[key] = {
            "n": len(durations),
            "min": min(durations),
            "p50": int(statistics.median(durations)),
            "p90": percentile(durations, 90),
            "max": max(durations),
            "last": max(r["end"] for r in group),
        }
    return stats


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


def running_rows(jobs, stats_server, stats_cluster, args):
    """One row per running job, with its ETA when the history allows one."""
    now = int(time.time())
    rows = []
    for job in jobs:
        if job["status"] not in RUNNING_STATES:
            continue
        elapsed = now - job["start"] if job["start"] else None
        entry = forecast(job, stats_server, stats_cluster, args.min_samples)
        row = {
            "cluster": job["cluster"],
            "server": job["server"],
            "task": job["task"],
            "status": job["status"],
            "start": job["start"],
            "elapsed": elapsed,
            "eta_p50": None,
            "eta_p90": None,
            "samples": entry["n"] if entry else 0,
            "scope": entry["scope"] if entry else None,
            "note": "",
        }
        if not entry:
            row["note"] = f"no history (<{args.min_samples} runs)"
        elif not job["start"]:
            row["note"] = "not started yet"
        else:
            row["eta_p50"] = job["start"] + entry["p50"]
            row["eta_p90"] = job["start"] + entry["p90"]
            if elapsed is not None and elapsed > entry["p90"]:
                row["note"] = "overdue: past the p90 of previous runs"
            elif entry["scope"] == "cluster" and args.by_server:
                row["note"] = "cluster-wide history"
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


def build_running_table(rows, args):
    table = Table(box=box.MINIMAL, expand=False,
                  title=None if args.watch else "Jobs running now",
                  title_style="bold green")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Server")
    table.add_column("Task", style="bold")
    table.add_column("Status", justify="center")
    table.add_column("Started")
    table.add_column("Elapsed", justify="right")
    table.add_column("ETA (median)", style="bold")
    table.add_column("ETA (p90)", style="yellow")
    table.add_column("Based on")

    previous = None
    for row in rows:
        if previous is not None and row["cluster"] != previous:
            table.add_section()
        cluster_cell = row["cluster"] if row["cluster"] != previous else ""
        previous = row["cluster"]
        style = "bold cyan" if row["status"] == "Running" else "yellow"
        based = f"{row['samples']} runs" if row["samples"] else "-"
        if row["note"]:
            colour = "red" if row["note"].startswith("overdue") else "dim"
            based = f"{based} [{colour}]({row['note']})[/{colour}]"
        table.add_row(
            cluster_cell,
            row["server"],
            row["task"],
            f"[{style}]{row['status']}[/{style}]",
            epoch_str(row["start"]),
            human_duration(row["elapsed"]),
            epoch_str(row["eta_p50"]),
            epoch_str(row["eta_p90"]),
            based,
        )
    return table


def snapshot(rm, clusters, args, path):
    jobs = collect_jobs(rm, clusters)
    if args.task:
        wanted = [t.lower() for t in args.task]
        jobs = [j for j in jobs if any(t in j["task"].lower() for t in wanted)]
    if args.server:
        jobs = [j for j in jobs if args.server.lower() in j["server"].lower()]

    records = load_history(path)
    new = [] if args.no_record else record_jobs(path, records, jobs)

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

    stats_server = aggregate(samples, by_server=True)
    stats_cluster = aggregate(samples, by_server=False)
    rows = running_rows(jobs, stats_server, stats_cluster, args)

    return {
        "new": new,
        "samples": samples,
        "stats": stats_server if args.by_server else stats_cluster,
        "running": rows,
    }


def render(result, args):
    renderables = []
    if not args.running:
        stats = result["stats"]
        renderables.append(build_stats_table(stats, args) if stats else
                           "[yellow]No job recorded yet - let this run for a "
                           "while before expecting an ETA.[/yellow]")
    if not args.stats:
        if renderables:
            renderables.append("")
        rows = result["running"]
        renderables.append(build_running_table(rows, args) if rows else
                           "[green]No job running.[/green]")
    if args.watch:
        header = (f"[bold green]Repman job ETA[/bold green] "
                  f"[dim]- {len(result['samples'])} samples - "
                  f"refresh {args.watch}s - {datetime.now():%H:%M:%S} - "
                  f"Ctrl+C to quit[/dim]")
        renderables.insert(0, header)
        renderables.insert(1, "")
    return Group(*renderables)


def as_json(result, args):
    stats = [{"cluster": k[0], "task": k[1],
              **({"server": k[2]} if args.by_server else {}), **v}
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
    parser.add_argument("--running", action="store_true",
                        help="Only the jobs running right now, with their ETA.")
    parser.add_argument("--stats", action="store_true",
                        help="Only the duration statistics.")
    parser.add_argument("--by-server", action="store_true",
                        help="Group the statistics per server instead of per cluster.")
    parser.add_argument("--since", metavar="DURATION",
                        help="Only use the runs of the last DURATION as samples, "
                             "e.g. 30d, 12h. Default: the whole history.")
    parser.add_argument("--min-samples", type=int, default=3, metavar="N",
                        help="Refuse to forecast below N past runs (default: 3).")
    parser.add_argument("--history", metavar="PATH",
                        help="History file (default: "
                             "$XDG_DATA_HOME/repman/job_history.jsonl).")
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
            running = len(result["running"])
            console.print(f"{datetime.now():%Y-%m-%d %H:%M:%S} "
                          f"recorded={len(result['new'])} "
                          f"history={len(result['samples'])} running={running}")
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
