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
Repman job explorer.

Lists the jobs/tasks replication-manager tracks per database server
(backups: mydumper / mariabackup / xtrabackup, reseeds, flashbacks,
binlog operations, ...) across ALL clusters by default.

  repman-jobs.py                       # every job of every cluster
  repman-jobs.py --running             # only what is running right now
  repman-jobs.py --failed              # only the jobs that ended in error
  repman-jobs.py --logs                # jobs table + the job log messages
  repman-jobs.py -v                    # only the job log messages (last 20)
  repman-jobs.py -v --log-limit 100    # the last 100 log messages
  repman-jobs.py -v -L 100             # same: with -v, -L limits the log lines
  repman-jobs.py -c mycluster -n 10       # live view of one cluster

Three filters are repeatable and behave as a union - give a value twice and
both are kept:
  repman-jobs.py -c mycluster -c othercluster  # these two clusters
  repman-jobs.py -w mydumper -w reseed # entries mentioning either word
  repman-jobs.py -v -l ERROR -l INFO   # log messages of either level
"""

import argparse
import os
import sys
import time
from datetime import datetime

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rich import box
from rich.console import Group
from rich.live import Live
from rich.markup import escape
from rich.table import Table

from lib.repman import (Repman, add_cluster_argument, console, epoch_str,
                        human_duration, level_style, load_config, parse_duration,
                        parse_ts, split_module)

# replication-manager job state codes (config.JobStateXxx)
STATE_LABELS = {
    0: "Queued",
    1: "Queued",
    2: "Running",
    3: "Running",
    4: "Success",
    5: "Failed",
    6: "Cancelled",
}
STATE_STYLES = {
    "Queued": "yellow",
    "Running": "bold cyan",
    "Success": "green",
    "Failed": "bold red",
    "Cancelled": "magenta",
    "Unknown": "dim",
}


def job_status(task):
    """Human status of a job, derived from its done flag and state code."""
    state = task.get("state")
    done = task.get("done")
    label = STATE_LABELS.get(state)
    if not done:
        # not finished yet: queued until it actually started
        return "Running" if task.get("start") else "Queued"
    if label in (None, "Queued", "Running"):
        return f"Done({state})" if state is not None else "Unknown"
    return label


def collect_jobs(rm, clusters):
    jobs = []
    for name in clusters:
        data = rm.get(f"clusters/{name}/jobs")
        if not data:
            continue
        for server_id, server in (data.get("servers") or {}).items():
            url = server.get("serverUrl") or server_id
            for task in server.get("tasks") or []:
                start = task.get("start") or 0
                end = task.get("end") or 0
                status = job_status(task)
                if status == "Running" and start:
                    duration = int(time.time()) - int(start)
                elif start and end:
                    duration = int(end) - int(start)
                else:
                    duration = None
                jobs.append({
                    "cluster": name,
                    "server": url,
                    "server_id": server_id,
                    "id": task.get("id"),
                    "task": task.get("task") or "-",
                    "runner": task.get("server") or "",
                    "port": task.get("port") or 0,
                    "status": status,
                    "start": start,
                    "end": end,
                    "duration": duration,
                    "result": (task.get("result") or "").strip(),
                })
    jobs.sort(key=lambda j: (j["cluster"], -(j["start"] or 0)))
    return jobs


def collect_job_logs(rm, clusters):
    """
    Job log lines of every selected cluster, from the per-cluster task buffer
    (logTask). It already carries both the [job] milestones and the
    step-by-step [backup] progress, ~200 lines deep per cluster.
    """
    entries, seen = [], set()

    def add(entry, cluster):
        ts = parse_ts(entry.get("timestamp"))
        text = (entry.get("text") or "").strip()
        if ts is None or not text:
            return
        key = (cluster, entry.get("timestamp"), text)
        if key in seen:
            return
        seen.add(key)
        module, message = split_module(text)
        entries.append({"ts": ts, "cluster": cluster, "module": module,
                        "level": (entry.get("level") or "").upper(),
                        "text": message})

    for name in clusters:
        data = rm.get(f"clusters/{name}")
        if not data:
            continue
        for entry in (data.get("logTask") or {}).get("buffer") or []:
            add(entry, name)

    entries.sort(key=lambda e: e["ts"])
    return entries


def job_haystack(job):
    """Text a -w/--word filter is matched against for a job row."""
    return " ".join(str(job.get(k) or "") for k in
                    ("cluster", "server", "task", "status", "result")).lower()


def build_table(jobs, args):
    table = Table(box=box.MINIMAL, expand=False,
                  title=None if args.watch else "Repman jobs",
                  title_style="bold green")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Server")
    table.add_column("Id", justify="right", style="dim")
    table.add_column("Task", style="bold")
    table.add_column("Status", justify="center")
    table.add_column("Start")
    table.add_column("End")
    table.add_column("Duration", justify="right")
    table.add_column("Result")

    previous = None
    for j in jobs:
        cluster_cell = j["cluster"] if j["cluster"] != previous else ""
        if previous is not None and j["cluster"] != previous:
            table.add_section()
        previous = j["cluster"]
        style = STATE_STYLES.get(j["status"], "white")
        result = j["result"] or ("running..." if j["status"] == "Running" else "-")
        if not args.full and len(result) > 60:
            result = result[:57] + "..."
        table.add_row(
            cluster_cell,
            j["server"],
            str(j["id"] if j["id"] is not None else "-"),
            j["task"],
            f"[{style}]{j['status']}[/{style}]",
            epoch_str(j["start"]),
            epoch_str(j["end"]) if j["end"] else "-",
            human_duration(j["duration"]),
            escape(result),
        )
    return table


def build_logs(entries, args):
    table = Table(box=box.MINIMAL, expand=False, title="Job log",
                  title_style="bold green", show_header=False)
    table.add_column("Time", style="dim", no_wrap=True)
    table.add_column("Cluster", style="cyan", no_wrap=True)
    table.add_column("Level", no_wrap=True)
    table.add_column("Module", style="blue", no_wrap=True)
    table.add_column("Message")
    for e in entries:
        style = level_style(e["level"])
        text = e["text"] if args.full or len(e["text"]) <= 160 else e["text"][:157] + "..."
        table.add_row(f"{e['ts']:%Y-%m-%d %H:%M:%S}", e["cluster"],
                      f"[{style}]{e['level']}[/{style}]", e["module"], escape(text))
    return table


def snapshot(rm, clusters, args, delta):
    # recomputed on every refresh so --since stays relative in watch mode
    since = datetime.now() - delta if delta else None
    jobs = [] if args.verbose else collect_jobs(rm, clusters)

    if args.running:
        jobs = [j for j in jobs if j["status"] in ("Running", "Queued")]
    if args.failed:
        jobs = [j for j in jobs if j["status"] in ("Failed", "Cancelled")]
    if args.task:
        wanted = [t.lower() for t in args.task]
        jobs = [j for j in jobs if any(t in j["task"].lower() for t in wanted)]
    if args.server:
        jobs = [j for j in jobs if args.server.lower() in j["server"].lower()]
    if args.word:
        # several -w are a union: an entry matching any of them is kept
        jobs = [j for j in jobs
                if any(w in job_haystack(j) for w in args.word)]
    if since:
        cutoff = since.timestamp()
        jobs = [j for j in jobs if (j["start"] or 0) >= cutoff]
    if args.limit:
        # N most recent overall, then back to the per-cluster display order
        jobs = sorted(jobs, key=lambda j: -(j["start"] or 0))[:args.limit]
        jobs.sort(key=lambda j: (j["cluster"], -(j["start"] or 0)))

    renderables = []
    count = f"{len(jobs)} job(s)"

    if not args.verbose:
        if jobs:
            renderables.append(build_table(jobs, args))
        else:
            renderables.append("[yellow]No job matching the current filters.[/yellow]")

    if args.logs or args.verbose:
        entries = collect_job_logs(rm, clusters)
        if since:
            entries = [e for e in entries if e["ts"] >= since]
        if args.task:
            wanted = [t.lower() for t in args.task]
            entries = [e for e in entries
                       if any(t in e["text"].lower() for t in wanted)]
        if args.server:
            entries = [e for e in entries if args.server.lower() in e["text"].lower()]
        if args.word:
            entries = [e for e in entries
                       if any(w in f"{e['module']} {e['text']}".lower() for w in args.word)]
        if args.level:
            entries = [e for e in entries if e["level"] in args.level]
        # with -v the jobs table is not built, so -L/--limit applies here
        log_limit = args.limit if (args.verbose and args.limit) else args.log_limit
        if log_limit:
            entries = entries[-log_limit:]
        if args.verbose:
            # -v: most recent line first
            entries.reverse()
        if renderables:
            renderables.append("")
        renderables.append(build_logs(entries, args) if entries
                           else "[yellow]No job log line.[/yellow]")
        if args.verbose:
            count = f"{len(entries)} log line(s)"

    if args.watch:
        header = (f"[bold green]Repman jobs[/bold green] "
                  f"[dim]- {count} - refresh {args.watch}s - "
                  f"{datetime.now():%H:%M:%S} - Ctrl+C to quit[/dim]")
        renderables.insert(0, header)
        renderables.insert(1, "")
    return Group(*renderables)


def main():
    parser = argparse.ArgumentParser(
        description="List the repman jobs (backups, reseeds, ...) of all clusters.")
    add_cluster_argument(parser)
    parser.add_argument("--running", action="store_true",
                        help="Only jobs currently running or queued.")
    parser.add_argument("--failed", action="store_true",
                        help="Only jobs that ended in error.")
    parser.add_argument("-t", "--task", action="append", default=[], metavar="NAME",
                        help="Keep only these task types, e.g. -t mydumper (repeatable).")
    parser.add_argument("--server", metavar="TEXT",
                        help="Keep only jobs of servers matching TEXT.")
    parser.add_argument("-w", "--word", action="append", default=[], metavar="TEXT",
                        help="Keep only entries containing TEXT, case insensitive "
                             "(repeatable: -w mydumper -w reseed keeps both).")
    parser.add_argument("-l", "--level", action="append", default=[], metavar="LEVEL",
                        help="Keep only log messages of these levels, e.g. -l INFO "
                             "-l ERROR (repeatable, log view only).")
    parser.add_argument("--since", metavar="DURATION",
                        help="Only jobs started less than DURATION ago, e.g. 12h, 7d.")
    parser.add_argument("-L", "--limit", type=int, default=0, metavar="N",
                        help="Keep only the N most recent jobs, or the N most recent "
                             "log messages with -v (0 = no limit, default).")
    parser.add_argument("-v", "--verbose", action="store_true",
                        help="Show the job log messages instead of the jobs table.")
    parser.add_argument("--logs", action="store_true",
                        help="Show the job log messages in addition to the jobs table.")
    parser.add_argument("--log-limit", type=int, default=20, metavar="N",
                        help="With -v/--logs: keep the N most recent lines "
                             "(default: 20, 0 = no limit). Overridden by -L with -v.")
    parser.add_argument("--full", action="store_true",
                        help="Do not truncate result and log messages.")
    parser.add_argument("-n", "--watch", type=int, nargs="?", const=10, default=0,
                        metavar="SECONDS",
                        help="Refresh continuously every SECONDS (default: 10).")
    parser.add_argument("--inline", action="store_true",
                        help="With -w: render in the scrollback instead of the alternate screen.")
    args = parser.parse_args()
    args.level = [l.upper() for l in args.level]
    args.word = [w.lower() for w in args.word]

    api, config_clusters = load_config()
    rm = Repman(api, config_clusters)
    clusters = rm.resolve_clusters(args.cluster)

    delta = parse_duration(args.since)

    if not args.watch:
        console.print(snapshot(rm, clusters, args, delta))
        return

    # Same Ctrl+C handling as repman-top.py: alternate screen by default so the
    # terminal is restored untouched, transient inline mode otherwise.
    alt_screen = not args.inline
    try:
        with Live(snapshot(rm, clusters, args, delta),
                  console=console,
                  auto_refresh=False,          # we drive the redraws ourselves
                  screen=alt_screen,
                  transient=not alt_screen,
                  vertical_overflow="crop") as live:
            while True:
                time.sleep(args.watch)
                live.update(snapshot(rm, clusters, args, delta), refresh=True)
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
