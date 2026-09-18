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
Repman server activity monitor - a `top` for replication-manager.

Live view of ALL clusters by default:
  * per cluster : topology, health, QPS, reads/s, writes/s, connections,
                  thread pool, data size
  * per server  : role/state, replication health, lag, QPS, reads/s,
                  writes/s, version, flags
  * open alerts : the errors and warnings currently raised by the monitor

  repman-top.py                # live, all clusters, refresh 5s
  repman-top.py -n 2           # refresh every 2 seconds
  repman-top.py -o             # single snapshot, no live loop
  repman-top.py -p             # add the running queries (process list)
  repman-top.py -c mycluster -p   # process list of one cluster only
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

from lib.repman import (Repman, add_cluster_argument, console, human_duration,
                        human_rate, human_size, load_config, nullable)

STATE_STYLES = {
    "Master": "bold green",
    "Slave": "green",
    "Slave Late": "yellow",
    "Slave Err": "bold red",
    "Slave Pending": "cyan",
    "StandAlone": "yellow",
    "Suspect": "bold yellow",
    "Failed": "bold red",
    "Maintenance": "magenta",
    "Rebuilding": "cyan",
    "Catching-up": "cyan",
    "Validating": "cyan",
    "Unconnected": "dim red",
}

# server flag -> (label, style); shown in the Flags column when true
FLAGS = [
    ("isMaintenance", "MAINT", "magenta"),
    ("ignored", "IGNORED", "dim"),
    ("prefered", "PREF", "blue"),
    ("preferedBackup", "PREF-BKP", "blue"),
    ("isDelayed", "DELAYED", "yellow"),
    ("isRelay", "RELAY", "cyan"),
    ("isVirtualMaster", "VMASTER", "cyan"),
    ("HaveSshError", "SSH-ERR", "bold red"),
    ("IsBackingUpBinaryLog", "BINLOG-BKP", "cyan"),
    ("InPurgingBinaryLog", "PURGE-BINLOG", "cyan"),
    ("IsInSlowQueryCapture", "SLOWCAP", "cyan"),
    ("IsInPFSQueryCapture", "PFSCAP", "cyan"),
    ("inCaptureMode", "CAPTURE", "cyan"),
]

CLUSTER_FLAGS = [
    ("isClusterDown", "CLUSTER-DOWN", "bold red"),
    ("isMasterDown", "MASTER-DOWN", "bold red"),
    ("isSplitBrain", "SPLIT-BRAIN", "bold red"),
    ("isLostMajority", "LOST-MAJORITY", "bold red"),
    ("isDown", "DOWN", "bold red"),
    ("isNotMonitoring", "NOT-MONITORED", "yellow"),
    ("isAlertDisable", "ALERTS-OFF", "yellow"),
    ("inPhysicalBackup", "PHYS-BACKUP", "cyan"),
    ("inLogicalBackup", "LOGIC-BACKUP", "cyan"),
    ("inBinlogBackup", "BINLOG-BACKUP", "cyan"),
    ("inResticBackup", "RESTIC-BACKUP", "cyan"),
    ("inRollingRestart", "ROLLING-RESTART", "yellow"),
    ("isProvision", "PROVISIONING", "yellow"),
    ("isNeedDatabasesRestart", "NEED-DB-RESTART", "yellow"),
    ("isNeedDatabasesConfigChange", "NEED-DB-CONFIG", "yellow"),
    ("isNeedProxiesRestart", "NEED-PROXY-RESTART", "yellow"),
]


def flag_list(payload, definitions):
    out = []
    for key, label, style in definitions:
        if payload.get(key):
            out.append(f"[{style}]{label}[/{style}]")
    return " ".join(out)


def server_lag(server):
    """Seconds behind master of the first replication channel, or None."""
    for repl in server.get("replications") or []:
        value = nullable(repl.get("secondsBehindMaster"), "Int64")
        if value is not None:
            return int(value)
    return None


# Counters split into reads and writes, taken from the status delta repman
# computes on its own monitoring loop (the delta also carries the window
# length in UPTIME, so we get a real per-second rate).
READ_VARS = ("COM_SELECT",)
WRITE_VARS = ("COM_INSERT", "COM_UPDATE", "COM_DELETE", "COM_REPLACE",
              "COM_INSERT_SELECT", "COM_UPDATE_MULTI", "COM_DELETE_MULTI",
              "COM_REPLACE_SELECT", "COM_LOAD")


def _counter(values, names):
    total = 0
    for name in names:
        try:
            total += int(values.get(name) or 0)
        except (TypeError, ValueError):
            pass
    return total


def server_rw(rm, cluster, server_id):
    """(reads/s, writes/s) of one server, or (None, None) when unavailable."""
    delta = rm.get(f"clusters/{cluster}/servers/{server_id}/status-delta", quiet=True)
    if not isinstance(delta, list):
        return None, None
    values = {v.get("variableName"): v.get("value")
              for v in delta if isinstance(v, dict)}
    window = _counter(values, ("UPTIME",))
    if window <= 0:
        return None, None
    return _counter(values, READ_VARS) / window, _counter(values, WRITE_VARS) / window


def health_style(health, is_error=False):
    """
    Style for a replicationHealth string. repman returns, among others:
    "Running OK", "Master OK", "Not a slave", "Running with delay",
    "NOT OK, ALL Stopped", "NOT OK, IO Stopped", "NOT OK, SQL Stopped".
    """
    if is_error:
        return "bold red"
    if not health or health == "-":
        return "dim"
    upper = health.upper()
    if ("NOT OK" in upper or "STOPPED" in upper or "ERR" in upper
            or "NOT CONNECTED" in upper):
        return "bold red"
    if upper.endswith("OK") or " OK" in upper or upper.startswith("OK"):
        return "green"
    if "DELAY" in upper or "LATE" in upper:
        return "yellow"
    return "dim"  # informational, e.g. "Not a slave" on a StandAlone node


def replication_errors(server):
    errors = []
    for repl in server.get("replications") or []:
        for key in ("lastIoError", "lastSqlError"):
            text = nullable(repl.get(key), "String")
            if text:
                errors.append(text)
    return errors


def fetch(rm, clusters, with_rw=True):
    """One snapshot: cluster payload + topology of every selected cluster."""
    snapshot = []
    for name in clusters:
        cluster = rm.get(f"clusters/{name}", quiet=True)
        if not cluster:
            snapshot.append({"name": name, "cluster": None, "servers": [], "alerts": None})
            continue
        servers = rm.get(f"clusters/{name}/topology/servers", quiet=True) or []
        alerts = rm.get(f"clusters/{name}/topology/alerts", quiet=True) or {}
        for server in servers:
            reads, writes = (server_rw(rm, name, server["id"])
                             if with_rw and server.get("id") else (None, None))
            server["_reads"], server["_writes"] = reads, writes
        snapshot.append({"name": name, "cluster": cluster, "servers": servers,
                         "alerts": alerts})
    return snapshot


def _sum_or_none(values):
    """Sum of the known values, or None when nothing is known."""
    known = [v for v in values if v is not None]
    return sum(known) if known else None


def build_clusters_table(snapshot):
    table = Table(box=box.MINIMAL, expand=False, title="Clusters",
                  title_style="bold green", title_justify="left")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Topology")
    table.add_column("Servers", justify="right")
    table.add_column("QPS", justify="right")
    table.add_column("R/s", justify="right", style="blue")
    table.add_column("W/s", justify="right", style="magenta")
    table.add_column("Conns", justify="right")
    table.add_column("CPU pool", justify="right")
    table.add_column("Data", justify="right")
    table.add_column("Index", justify="right")
    table.add_column("Failovers", justify="right")
    table.add_column("Uptime", justify="right")
    table.add_column("Status")

    for item in snapshot:
        cluster = item["cluster"]
        if cluster is None:
            table.add_row(item["name"], *["-"] * 11, "[yellow]API unreachable[/yellow]")
            continue
        workload = cluster.get("workLoad") or {}
        servers = item["servers"]
        up = sum(1 for s in servers if s.get("state") not in ("Failed", "Suspect", ""))
        flags = flag_list(cluster, CLUSTER_FLAGS)
        if not flags:
            flags = "[green]OK[/green]" if cluster.get("isAllDbUp") else "[yellow]DEGRADED[/yellow]"
        cpu = workload.get("cpuThreadPool")
        qps = workload.get("qps") or 0
        conns = workload.get("connections")
        reads = _sum_or_none(s.get("_reads") for s in servers)
        writes = _sum_or_none(s.get("_writes") for s in servers)
        table.add_row(
            item["name"],
            cluster.get("topology") or "-",
            f"{up}/{len(servers)}",
            f"{qps:,}".replace(",", " "),
            human_rate(reads),
            human_rate(writes),
            str(conns) if conns is not None else "-",
            f"{cpu:.0f}%" if isinstance(cpu, (int, float)) else "-",
            human_size(workload.get("dbTableSize")),
            human_size(workload.get("dbIndexSize")),
            str(cluster.get("failoverCounter", 0)),
            cluster.get("uptime") or "-",
            flags,
        )
    return table


def build_servers_table(snapshot, show_cluster):
    table = Table(box=box.MINIMAL, expand=False, title="Servers",
                  title_style="bold green", title_justify="left")
    if show_cluster:
        table.add_column("Cluster", style="bold cyan")
    table.add_column("Server")
    table.add_column("State", justify="center")
    table.add_column("Replication")
    table.add_column("Lag", justify="right")
    table.add_column("QPS", justify="right")
    table.add_column("R/s", justify="right", style="blue")
    table.add_column("W/s", justify="right", style="magenta")
    table.add_column("RO", justify="center")
    table.add_column("Version")
    table.add_column("Binlog")
    table.add_column("Flags")

    first_cluster = True
    for item in snapshot:
        servers = sorted(item["servers"],
                         key=lambda s: (s.get("state") != "Master", s.get("url") or ""))
        if not servers:
            continue
        if not first_cluster:
            table.add_section()
        first_cluster = False

        for idx, s in enumerate(servers):
            state = s.get("state") or "Unknown"
            style = STATE_STYLES.get(state, "white")
            lag = server_lag(s)
            if lag is None:
                lag_cell = "-"
            elif lag == 0:
                lag_cell = "[green]0s[/green]"
            else:
                lag_style = "yellow" if lag < 60 else "bold red"
                lag_cell = f"[{lag_style}]{human_duration(lag)}[/{lag_style}]"

            health = s.get("replicationHealth") or "-"
            errors = replication_errors(s)
            if errors:
                health = errors[0]
            style_health = health_style(health, is_error=bool(errors))

            version = s.get("dbVersion") or {}
            version_cell = "-"
            if version.get("flavor"):
                version_cell = (f"{version['flavor']} {version.get('major', '')}."
                                f"{version.get('minor', '')}.{version.get('release', '')}")

            binlog = s.get("binaryLogFile") or "-"
            if binlog != "-" and s.get("binaryLogFilesCount"):
                binlog = f"{binlog} ({s['binaryLogFilesCount']})"

            read_only = "ON" if str(s.get("readOnly", "")).upper() == "ON" else "off"
            ro_style = "yellow" if read_only == "ON" else "dim"

            row = []
            if show_cluster:
                row.append(item["name"] if idx == 0 else "")
            row += [
                s.get("url") or s.get("host") or "-",
                f"[{style}]{state}[/{style}]",
                f"[{style_health}]{escape(health[:48])}[/{style_health}]",
                lag_cell,
                str(s.get("qps", 0)),
                human_rate(s.get("_reads")),
                human_rate(s.get("_writes")),
                f"[{ro_style}]{read_only}[/{ro_style}]",
                version_cell,
                binlog,
                flag_list(s, FLAGS),
            ]
            table.add_row(*row)
    return table


def build_alerts_table(snapshot):
    rows = []
    for item in snapshot:
        alerts = item["alerts"] or {}
        for kind, style in (("errors", "bold red"), ("warnings", "yellow")):
            for alert in alerts.get(kind) or []:
                rows.append((item["name"], style, alert.get("number") or "-",
                             alert.get("from") or "-", alert.get("desc") or ""))
    if not rows:
        return None
    table = Table(box=box.MINIMAL, expand=False, title="Open alerts",
                  title_style="bold green", title_justify="left")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Code")
    table.add_column("From", style="dim")
    table.add_column("Description")
    for cluster, style, number, origin, desc in rows:
        table.add_row(cluster, f"[{style}]{escape(number)}[/{style}]", origin, escape(desc))
    return table


def build_processlist_table(rm, snapshot, args):
    table = Table(box=box.MINIMAL, expand=False, title="Running queries",
                  title_style="bold green", title_justify="left")
    table.add_column("Cluster", style="bold cyan")
    table.add_column("Server")
    table.add_column("Id", justify="right", style="dim")
    table.add_column("User")
    table.add_column("Host")
    table.add_column("Db")
    table.add_column("Command")
    table.add_column("Time", justify="right")
    table.add_column("State")
    table.add_column("Query")

    rows = []
    for item in snapshot:
        for s in item["servers"]:
            server_id = s.get("id")
            if not server_id:
                continue
            threads = rm.get(f"clusters/{item['name']}/servers/{server_id}/processlist",
                             quiet=True) or []
            for t in threads:
                command = t.get("command") or ""
                info = nullable(t.get("info"), "String") or ""
                if not args.all_threads:
                    if command in ("Sleep", "Daemon", "Binlog Dump") or command.startswith("Slave_"):
                        continue
                    if not info:
                        continue
                seconds = nullable(t.get("time"), "Float64")
                rows.append({
                    "cluster": item["name"],
                    "server": s.get("url") or s.get("host") or "-",
                    "id": t.get("id"),
                    "user": t.get("user") or "",
                    "host": t.get("host") or "",
                    "db": nullable(t.get("db"), "String") or "",
                    "command": command,
                    "time": seconds,
                    "state": nullable(t.get("state"), "String") or "",
                    "info": " ".join(info.split()),
                })

    rows.sort(key=lambda r: (r["time"] or 0), reverse=True)
    if args.limit:
        rows = rows[:args.limit]
    if not rows:
        return "[dim]No running query.[/dim]"

    for r in rows:
        seconds = r["time"]
        style = "white"
        if seconds is not None:
            style = "bold red" if seconds >= 60 else ("yellow" if seconds >= 5 else "white")
        query = r["info"] if args.full or len(r["info"]) <= 70 else r["info"][:67] + "..."
        table.add_row(
            r["cluster"], r["server"], str(r["id"]), r["user"], r["host"], r["db"],
            r["command"],
            f"[{style}]{human_duration(seconds) if seconds is not None else '-'}[/{style}]",
            escape(r["state"][:32]),
            escape(query),
        )
    return table


def render(rm, clusters, args, monitor_info):
    snapshot = fetch(rm, clusters, with_rw=not args.no_rw)
    header = (f"[bold green]repman-top[/bold green] "
              f"[dim]{monitor_info} - {len(clusters)} cluster(s) - "
              f"{datetime.now():%Y-%m-%d %H:%M:%S}"
              f"{'' if args.once else f' - refresh {args.interval}s - Ctrl+C to quit'}[/dim]")

    parts = [header, ""]
    parts.append(build_clusters_table(snapshot))
    parts.append("")
    parts.append(build_servers_table(snapshot, show_cluster=len(clusters) > 1))

    if not args.no_alerts:
        alerts = build_alerts_table(snapshot)
        if alerts is not None:
            parts.append("")
            parts.append(alerts)

    if args.processlist:
        parts.append("")
        parts.append(build_processlist_table(rm, snapshot, args))

    return Group(*parts)


def main():
    parser = argparse.ArgumentParser(
        description="Live server activity of every repman cluster (top-like).")
    add_cluster_argument(parser)
    parser.add_argument("-n", "--interval", type=float, default=5, metavar="SECONDS",
                        help="Refresh interval in seconds (default: 5).")
    parser.add_argument("-o", "--once", action="store_true",
                        help="Print a single snapshot and exit.")
    parser.add_argument("-p", "--processlist", action="store_true",
                        help="Also show the running queries of every server.")
    parser.add_argument("-a", "--all-threads", action="store_true",
                        help="With -p: include idle/system threads (Sleep, Slave_IO, ...).")
    parser.add_argument("--limit", type=int, default=25, metavar="N",
                        help="With -p: max queries displayed (default: 25, 0 = no limit).")
    parser.add_argument("--no-alerts", action="store_true",
                        help="Hide the open alerts table.")
    parser.add_argument("--no-rw", action="store_true",
                        help="Hide the read/write rates (saves one API call per server).")
    parser.add_argument("--inline", action="store_true",
                        help="Render in the scrollback instead of the alternate screen.")
    parser.add_argument("--full", action="store_true",
                        help="Do not truncate queries.")
    args = parser.parse_args()

    api, config_clusters = load_config()
    rm = Repman(api, config_clusters)
    clusters = rm.resolve_clusters(args.cluster)

    monitor = rm.get("monitor", quiet=True) or {}
    monitor_info = f"{monitor.get('hostname', '?')} {monitor.get('fullVersion', '')}".strip()

    if args.once:
        console.print(render(rm, clusters, args, monitor_info))
        return

    # Alternate screen by default (like top): the terminal is restored as it
    # was on exit. Inline mode keeps the flow in the scrollback but stays
    # transient, so Ctrl+C erases the live region instead of redrawing it.
    alt_screen = not args.inline
    try:
        with Live(render(rm, clusters, args, monitor_info),
                  console=console,
                  auto_refresh=False,          # we drive the redraws ourselves
                  screen=alt_screen,
                  transient=not alt_screen,
                  vertical_overflow="crop") as live:
            while True:
                time.sleep(args.interval)
                live.update(render(rm, clusters, args, monitor_info), refresh=True)
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
