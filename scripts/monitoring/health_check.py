#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "requests",
#     "rich",
#     "pyyaml",
# ]
# ///

import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from rich.live import Live
from rich.table import Table
from rich import box

from lib.repman import Repman, console, load_config, server_host

API, CONFIG_CLUSTERS = load_config()

# Clusters, hosts and expected masters are discovered from the API on every
# refresh; the 'clusters' section of config.yaml is optional and only used to
# override what the API reports; the client applies it on its own (see
# lib.repman.build_cluster_definition).

SLAVE_PROGRESS_STATES = ["Slave Pending", "Rebuilding", "Catching-up", "Validating"]
SLAVE_STATES = ["Slave", "StandAlone"]
REFRESH_SECONDS = 30


def update_state_file(cluster_name, message):
    state_file = f"/var/tmp/repman-state-{cluster_name}"
    try:
        with open(state_file, "w") as f:
            f.write(message + "\n")
    except IOError:
        pass


def get_servers_topology(rm, cluster_name):
    return rm.get(f"clusters/{cluster_name}/topology/servers", quiet=True)


def check_host(definition, host, actual):
    """(status, expected role) of one host: ok / progress / unknown / error."""
    master = definition["master"]
    if master:
        expected = "Master" if host == master else "Slave or StandAlone"
    else:
        # No prefered master declared in repman: any role is acceptable per
        # host, the one-master-per-cluster invariant is checked globally.
        expected = "Master or Slave"
    if actual in (None, "", "Unknown", "Fetching..."):
        return "unknown", expected
    if master and host == master:
        return ("ok" if actual == "Master" else "error"), expected
    accepted = SLAVE_STATES if master else SLAVE_STATES + ["Master"]
    if actual in accepted:
        return "ok", expected
    return ("progress" if actual in SLAVE_PROGRESS_STATES else "error"), expected


def cluster_verdict(definition, state):
    """Error message for one cluster, or None when everything is as expected."""
    master = definition["master"]
    if not definition["hosts"]:
        return "No server reported by the API"
    if not master:
        masters = [h for h in definition["hosts"] if state.get(h) == "Master"]
        if len(masters) != 1:
            return (f"{len(masters)} host(s) in 'Master' state "
                    f"({', '.join(masters) or 'none'}). Exactly one is expected")
    for host in definition["hosts"]:
        status, expected = check_host(definition, host, state.get(host))
        if status == "error":
            return f"{host} is {state.get(host)}. It should be '{expected}'"
    return None


def generate_global_table(clusters_def, all_clusters_status):
    table = Table(box=box.MINIMAL, expand=False)

    table.add_column("Cluster", justify="left", style="bold cyan")
    table.add_column("Host / Server", justify="left", style="white")
    table.add_column("Expected role", justify="center", style="magenta")
    table.add_column("Current state", justify="center")

    colors = {"ok": "green", "progress": "cyan", "unknown": "yellow", "error": "red"}

    for idx, c in enumerate(clusters_def):
        c_name = c["name"]
        hosts_status = all_clusters_status.get(c_name, {})

        if idx > 0:
            table.add_row("", "", "", "", "")

        if not c["hosts"]:
            table.add_row(c_name, "-", "-", "[yellow]No server[/yellow]")
            continue

        for h_idx, host in enumerate(c["hosts"]):
            cluster_cell = c_name if h_idx == 0 else ""
            actual = hosts_status.get(host, "Unknown")
            status, expected = check_host(c, host, actual)
            color = colors[status]
            table.add_row(cluster_cell, host, expected, f"[{color}]{actual or 'Unknown'}[/{color}]")

    return table


# --- Authentication (the client re-logs in on its own when the token expires) ---
rm = Repman(API, CONFIG_CLUSTERS)

clusters_def = rm.collect_clusters()
if not clusters_def:
    console.print("[bold red]Error: no cluster found on the API.[/bold red]")
    sys.exit(1)

# State dictionaries, indexed by cluster
old_states = {}
display_states = {c["name"]: {h: "Fetching..." for h in c["hosts"]} for c in clusters_def}

console.print("[bold green]Multi-cluster monitoring started.[/bold green]\n")

try:
    with Live(generate_global_table(clusters_def, display_states), refresh_per_second=1) as live:
        while True:
            # Re-discover on every pass: a cluster added to (or removed from)
            # repman shows up without restarting the script.
            names = rm.cluster_names() or [c["name"] for c in clusters_def]
            previous = {c["name"]: c for c in clusters_def}
            new_defs, new_states = [], {}

            for name in names:
                servers = get_servers_topology(rm, name)

                if servers is None:
                    # API hiccup: keep the last known topology and display
                    definition = previous.get(name)
                    if definition is None:
                        continue
                    new_defs.append(definition)
                    new_states[name] = display_states.get(name, {})
                    continue

                definition = rm.cluster_definition(name, servers)
                new_defs.append(definition)

                by_host = {server_host(s): s.get("state") for s in servers}
                state = {host: by_host.get(host, "Not found") for host in definition["hosts"]}
                new_states[name] = state

                has_changed = bool(old_states.get(name)) and any(
                    state.get(host) != old_states[name].get(host)
                    for host in definition["hosts"])
                old_states[name] = state.copy()

                if has_changed:
                    update_state_file(name, "Recent change. Skipping.")
                    continue

                update_state_file(name, cluster_verdict(definition, state) or "OK")

            clusters_def = new_defs
            display_states = new_states
            old_states = {n: s for n, s in old_states.items() if n in new_states}

            live.update(generate_global_table(clusters_def, display_states))

            time.sleep(REFRESH_SECONDS)

except KeyboardInterrupt:
    sys.exit(0)
