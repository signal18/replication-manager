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
Repman event explorer.

Merges every event source the replication-manager API exposes:
  * cluster general log      (log.buffer)
  * cluster task / job log   (logTask.buffer)
  * cluster SQL error log    (sqlErrorLog.buffer)
  * global monitor log       (/api/monitor -> logs.buffer, includes DEBUG)

By default it targets ALL clusters and prints the complete merged feed.
With -t/--transitions it falls back to the correlation view: node state
changes (Master/Slave/Suspect/...) with the events that surround them.

Note: replication-manager only keeps a small in-memory ring buffer per
source (~200 lines for the cluster log, ~80 for the monitor log), so the
history depth is bounded by the daemon, not by this tool.
"""

import argparse
import os
import re
import sys
from datetime import datetime, timedelta

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rich.markup import escape

from lib.repman import (Repman, add_cluster_argument, console, level_style,
                        load_config, parse_duration, parse_ts, split_module)

TRANSITION_RE = re.compile(
    r"Server (?P<host>[^\s:]+)(?::\d+)? state transition from (?P<from>\S+) changed to: (?P<to>\S+)"
)
PREV_STATE_RE = re.compile(r"Server [^\s:]+(?::\d+)? previous state set to: \S+")

# source name -> key of the buffer inside the cluster payload
CLUSTER_SOURCES = {
    "general": "log",
    "job": "logTask",
    "sqlerror": "sqlErrorLog",
}
ALL_SOURCES = list(CLUSTER_SOURCES) + ["monitor"]


def collect(rm, clusters, sources, explicit_clusters):
    """Return the merged, de-duplicated event list of the given clusters."""
    events, seen = [], set()

    def add(entry, cluster, source):
        ts = parse_ts(entry.get("timestamp"))
        text = (entry.get("text") or "").strip()
        if ts is None or not text:
            return
        key = (cluster, entry.get("timestamp"), text)
        if key in seen:
            return
        seen.add(key)
        module, message = split_module(text)
        events.append({
            "ts": ts,
            "cluster": cluster,
            "source": source,
            "level": (entry.get("level") or "").upper(),
            "module": module,
            "text": message,
            "raw": text,
        })

    wanted = [s for s in CLUSTER_SOURCES if s in sources]
    for name in (clusters if wanted else []):
        data = rm.get(f"clusters/{name}")
        if not data:
            continue
        for source in wanted:
            buffer = (data.get(CLUSTER_SOURCES[source]) or {}).get("buffer") or []
            for entry in buffer:
                add(entry, name, source)

    if "monitor" in sources:
        monitor = rm.get("monitor")
        for entry in ((monitor or {}).get("logs") or {}).get("buffer") or []:
            group = entry.get("group") or ""
            if group:
                if group not in clusters:
                    continue
            elif explicit_clusters:
                # daemon-wide line: keep it only when no cluster was requested
                continue
            add(entry, group or "-", "monitor")

    events.sort(key=lambda e: (e["ts"], e["cluster"]))
    return events


def apply_filters(events, args, since):
    def keep(e):
        if since and e["ts"] < since:
            return False
        if args.level and e["level"] not in args.level:
            return False
        if args.module and e["module"].lower() not in args.module:
            return False
        if args.host and args.host.lower() not in e["raw"].lower():
            return False
        if args.filter and not all(f.lower() in e["raw"].lower() for f in args.filter):
            return False
        if args.exclude and any(f.lower() in e["raw"].lower() for f in args.exclude):
            return False
        return True

    return [e for e in events if keep(e)]


def render_line(e, show_cluster, width_cluster, width_module):
    parts = [f"[dim]{e['ts']:%Y-%m-%d %H:%M:%S}[/dim]"]
    if show_cluster:
        parts.append(f"[cyan]{e['cluster']:<{width_cluster}}[/cyan]")
    style = level_style(e["level"])
    parts.append(f"[{style}]{e['level']:<5}[/{style}]")
    parts.append(f"[blue]{e['module']:<{width_module}}[/blue]")
    parts.append(escape(e["text"]))
    console.print(" ".join(parts), highlight=False)


def stream_mode(events, args):
    if not events:
        console.print("[yellow]No event matching the current filters.[/yellow]")
        return
    if args.limit:
        events = events[-args.limit:]
    show_cluster = len({e["cluster"] for e in events}) > 1 or len(args.cluster) != 1
    width_cluster = max((len(e["cluster"]) for e in events), default=7)
    width_module = min(max((len(e["module"]) for e in events), default=7), 10)
    console.print(f"[bold green]{len(events)} event(s)[/bold green] "
                  f"[dim]from {events[0]['ts']:%Y-%m-%d %H:%M:%S} "
                  f"to {events[-1]['ts']:%Y-%m-%d %H:%M:%S}[/dim]\n")
    for e in reversed(events):  # newest first
        render_line(e, show_cluster, width_cluster, width_module)


def find_causes(pool, transition, before, after):
    lo = transition["ts"] - timedelta(minutes=before)
    hi = transition["ts"] + timedelta(minutes=after)
    causes = []
    for e in pool:
        if not (lo <= e["ts"] <= hi):
            continue
        m = TRANSITION_RE.search(e["raw"])
        if (m and e["ts"] == transition["ts"]
                and m.group("host") == transition["host"]
                and m.group("to") == transition["to"]):
            continue  # the transition itself
        causes.append(e)
    causes.sort(key=lambda e: e["ts"], reverse=True)
    return causes


def transitions_mode(events, args):
    by_cluster = {}
    for e in events:
        by_cluster.setdefault(e["cluster"], []).append(e)

    found_any = False
    for cluster, entries in by_cluster.items():
        transitions = []
        for e in entries:
            m = TRANSITION_RE.search(e["raw"])
            if not m:
                continue
            if args.host and args.host.lower() not in m.group("host").lower():
                continue
            transitions.append({"ts": e["ts"], "host": m.group("host"),
                                "from": m.group("from"), "to": m.group("to")})
        if not transitions:
            continue
        found_any = True

        pool = [e for e in entries if not PREV_STATE_RE.search(e["raw"])]
        transitions = transitions[-args.limit:][::-1] if args.limit else transitions[::-1]

        console.print(f"[bold green]State changes - cluster '{cluster}'[/bold green] "
                      f"({len(transitions)} transition(s), window -{args.before}min/+{args.after}min)\n")

        for t in transitions:
            t["causes"] = find_causes(pool, t, args.before, args.after)

        # Merge consecutive transitions sharing the exact same cause list
        # (concurrent events across several nodes).
        groups = []
        for t in transitions:
            sig = tuple((c["ts"], c["raw"]) for c in t["causes"])
            if groups and groups[-1]["sig"] == sig:
                groups[-1]["transitions"].append(t)
            else:
                groups.append({"sig": sig, "transitions": [t], "causes": t["causes"]})

        for g in groups:
            for t in g["transitions"]:
                console.print(f"[bold]{t['ts']:%Y-%m-%d %H:%M:%S}[/bold]  [cyan]{t['host']}[/cyan]  "
                              f"[yellow]{t['from']}[/yellow] -> [green]{t['to']}[/green]",
                              highlight=False)
            if not g["causes"]:
                console.print("    [dim]- no correlated event found in the window -[/dim]")
            else:
                for c in g["causes"]:
                    style = level_style(c["level"])
                    console.print(f"    [dim]{c['ts']:%Y-%m-%d %H:%M:%S}[/dim] "
                                  f"[{style}]{c['level']:<5}[/{style}] "
                                  f"[blue]{c['module']}[/blue] {escape(c['text'])}",
                                  highlight=False)
            console.print()

    if not found_any:
        console.print("[yellow]No node state change found in the log buffers.[/yellow]")


def main():
    parser = argparse.ArgumentParser(
        description="Explore repman events: full merged feed by default, "
                    "or node state changes correlated with their causes (-t).")
    add_cluster_argument(parser)
    parser.add_argument("-t", "--transitions", action="store_true",
                        help="Correlation view: node state changes + surrounding events.")
    parser.add_argument("--host", metavar="TEXT",
                        help="Keep only events mentioning this node (substring of name/IP).")
    parser.add_argument("-s", "--source", action="append", default=[], choices=ALL_SOURCES,
                        help=f"Event source (repeatable). Default: all ({', '.join(ALL_SOURCES)}).")
    parser.add_argument("-L", "--level", action="append", default=[], metavar="LEVEL",
                        help="Keep only these levels, e.g. -L ERROR -L STATE (repeatable).")
    parser.add_argument("-m", "--module", action="append", default=[], metavar="MODULE",
                        help="Keep only these modules, e.g. -m general -m backup (repeatable).")
    parser.add_argument("-f", "--filter", action="append", default=[], metavar="TEXT",
                        help="Keep messages containing TEXT (repeatable, AND).")
    parser.add_argument("-F", "--exclude", action="append", default=[], metavar="TEXT",
                        help="Drop messages containing TEXT (repeatable, OR).")
    parser.add_argument("--since", metavar="DURATION",
                        help="Only events younger than DURATION, e.g. 30m, 6h, 2d.")
    parser.add_argument("-l", "--limit", type=int, default=0, metavar="N",
                        help="Keep only the N most recent entries (0 = no limit, default).")
    parser.add_argument("--before", type=int, default=5, metavar="MIN",
                        help="Transitions mode: minutes to look back (default: 5).")
    parser.add_argument("--after", type=int, default=1, metavar="MIN",
                        help="Transitions mode: minutes to look forward (default: 1).")
    args = parser.parse_args()

    args.level = [l.upper() for l in args.level]
    args.module = [m.lower() for m in args.module]
    sources = args.source or ALL_SOURCES
    if args.transitions and not args.limit:
        args.limit = 20

    api, config_clusters = load_config()
    rm = Repman(api, config_clusters)
    clusters = rm.resolve_clusters(args.cluster)

    since = None
    delta = parse_duration(args.since)
    if delta:
        since = datetime.now() - delta

    events = collect(rm, clusters, sources, bool(args.cluster))
    events = apply_filters(events, args, since)

    if args.transitions:
        transitions_mode(events, args)
    else:
        stream_mode(events, args)


if __name__ == "__main__":
    main()
