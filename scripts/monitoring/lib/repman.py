"""
Shared helpers for the repman-* CLI tools.

Every repman-*.py inserts its own directory at the head of sys.path before
importing this module, so the tools stay runnable from anywhere with
`uv run`.
"""

import os
import re
import sys
from datetime import datetime, timedelta

import requests
import urllib3
import yaml
from rich.console import Console

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

console = Console()

TS_FMT = "%Y/%m/%d %H:%M:%S"
MODULE_RE = re.compile(r"^\s*\[(?P<module>[A-Za-z0-9_.-]+)\]\s*")
DURATION_RE = re.compile(r"^(?P<value>\d+)(?P<unit>[smhdw])$", re.IGNORECASE)

LEVEL_STYLES = {
    "ERROR": "bold red",
    "STATE": "bold magenta",
    "WARN": "yellow",
    "WARNING": "yellow",
    "ALERT": "bold red",
    "INFO": "white",
    "NOTICE": "cyan",
    "DEBUG": "dim",
}


def level_style(level):
    return LEVEL_STYLES.get((level or "").upper(), "white")


def config_path():
    """
    Locate config.yaml, in order of precedence:
      1. $REPMAN_CONFIG
      2. ./config.yaml (current directory)
      3. $XDG_CONFIG_HOME/repman/config.yaml (default ~/.config/repman)
      4. next to the scripts
    """
    override = os.environ.get("REPMAN_CONFIG")
    if override:
        if os.path.exists(override):
            return override
        console.print(f"[bold red]Error: REPMAN_CONFIG points to '{override}', "
                      f"which does not exist.[/bold red]")
        sys.exit(1)

    xdg = os.environ.get("XDG_CONFIG_HOME") or os.path.join(os.path.expanduser("~"), ".config")
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    candidates = [
        os.path.join(os.getcwd(), "config.yaml"),
        os.path.join(xdg, "repman", "config.yaml"),
        os.path.join(root, "config.yaml"),
    ]
    for path in candidates:
        if os.path.exists(path):
            return path
    console.print("[bold red]Error: 'config.yaml' not found.[/bold red] Looked in:\n  "
                  + "\n  ".join(candidates)
                  + "\n[dim]Set REPMAN_CONFIG=/path/to/config.yaml to override.[/dim]")
    sys.exit(1)


def load_config():
    path = config_path()
    try:
        with open(path, "r") as f:
            config = yaml.safe_load(f) or {}
    except Exception as e:
        console.print(f"[bold red]Error reading {path}: {e}[/bold red]")
        sys.exit(1)
    if "api" not in config:
        console.print(f"[bold red]Error: missing 'api' section in {path}.[/bold red]")
        sys.exit(1)
    return config["api"], config.get("clusters") or []


def parse_duration(text):
    """'90m', '2h', '3d' -> timedelta. Returns None when text is falsy."""
    if not text:
        return None
    m = DURATION_RE.match(text.strip())
    if not m:
        console.print(f"[bold red]Invalid duration '{text}' (expected e.g. 30m, 2h, 3d).[/bold red]")
        sys.exit(1)
    units = {"s": "seconds", "m": "minutes", "h": "hours", "d": "days", "w": "weeks"}
    return timedelta(**{units[m.group("unit").lower()]: int(m.group("value"))})


def split_module(text):
    """Split a '[module] message' log line into (module, message)."""
    m = MODULE_RE.match(text or "")
    if not m:
        return "", (text or "").strip()
    return m.group("module"), text[m.end():].strip()


def nullable(value, key=None):
    """Unwrap Go sql.NullXxx JSON objects ({'String': .., 'Valid': ..})."""
    if not isinstance(value, dict):
        return value
    if not value.get("Valid", False):
        return None
    if key and key in value:
        return value[key]
    for k in ("String", "Int64", "Float64", "Bool", "Time"):
        if k in value:
            return value[k]
    return None


def human_duration(seconds):
    if seconds is None:
        return "-"
    seconds = int(seconds)
    sign = "-" if seconds < 0 else ""
    seconds = abs(seconds)
    d, rem = divmod(seconds, 86400)
    h, rem = divmod(rem, 3600)
    m, s = divmod(rem, 60)
    if d:
        return f"{sign}{d}d{h:02d}h"
    if h:
        return f"{sign}{h}h{m:02d}m"
    if m:
        return f"{sign}{m}m{s:02d}s"
    return f"{sign}{s}s"


def human_size(num):
    if not num:
        return "-"
    units = ["B", "K", "M", "G", "T", "P"]
    value = float(num)
    for unit in units:
        if value < 1024 or unit == units[-1]:
            return f"{value:.0f}{unit}" if unit == "B" else f"{value:.1f}{unit}"
        value /= 1024
    return str(num)


def human_rate(value):
    """Compact per-second rate: 3287.4 -> '3.3k', 190.2 -> '190', 4.05 -> '4.1'."""
    if value is None:
        return "-"
    if value >= 10000:
        return f"{value / 1000:.1f}k"
    if value >= 100:
        return f"{value:.0f}"
    if value >= 10:
        return f"{value:.1f}"
    if value > 0:
        return f"{value:.2f}"
    return "0"


def epoch_str(value):
    if not value:
        return "-"
    try:
        return datetime.fromtimestamp(int(value)).strftime("%Y-%m-%d %H:%M:%S")
    except (ValueError, OSError, OverflowError):
        return str(value)


def server_host(server):
    """Hostname of a topology server ('host', falling back to 'url')."""
    host = (server.get("host") or "").strip()
    if host:
        return host
    url = (server.get("url") or "").strip()
    return url.rsplit(":", 1)[0] if ":" in url else url


def cluster_overrides(config_clusters):
    """Index the optional 'clusters' section of config.yaml by lower name."""
    return {c["name"].lower(): c
            for c in (config_clusters or []) if isinstance(c, dict) and c.get("name")}


def build_cluster_definition(name, servers, override=None):
    """Expected topology of one cluster, derived from its topology/servers.

    Returns {"name", "master", "hosts"}. The expected master is the server
    repman itself flags as 'prefered' (its db-servers-prefered-master); it
    stays None when the cluster declares none, and callers then only check
    that exactly one server holds the Master role. An optional override, one
    entry of the now optional 'clusters' section of config.yaml, wins over
    whatever the API reports.
    """
    override = override or {}
    hosts, master = [], None
    for server in servers or []:
        host = server_host(server)
        if not host or host in hosts:
            continue
        hosts.append(host)
        if server.get("prefered") and master is None:
            master = host
    if override.get("hosts"):
        hosts = list(override["hosts"])
    if override.get("master"):
        master = override["master"]
    if master:
        hosts = [master] + [h for h in hosts if h != master]
    return {"name": name, "master": master, "hosts": hosts}


class Repman:
    """Thin, read-only client for the replication-manager REST API."""

    def __init__(self, api, config_clusters=None, timeout=30):
        self.url = api["url"].rstrip("/")
        self.overrides = cluster_overrides(config_clusters)
        self.username = api["username"]
        self.password = api["password"]
        self.timeout = timeout
        self.session = requests.Session()
        self.session.verify = False
        self.token = None
        self.login()

    def login(self):
        try:
            r = self.session.post(
                f"{self.url}/api/login",
                headers={"Content-Type": "application/json", "Accept": "text/html"},
                json={"username": self.username, "password": self.password},
                timeout=self.timeout,
            )
            r.raise_for_status()
        except requests.RequestException as e:
            console.print(f"[bold red]Authentication failed: {e}[/bold red]")
            sys.exit(1)
        self.token = r.text.strip()

    def get(self, path, quiet=False):
        """GET /api/<path>. Retries once after a fresh login on 401/403."""
        last_error = None
        for attempt in (1, 2):
            try:
                r = self.session.get(
                    f"{self.url}/api/{path.lstrip('/')}",
                    headers={"Accept": "application/json",
                             "Authorization": f"Bearer {self.token}"},
                    timeout=self.timeout,
                )
                if r.status_code in (401, 403) and attempt == 1:
                    self.login()
                    continue
                r.raise_for_status()
                return r.json()
            except (requests.RequestException, ValueError) as e:
                last_error = e
                if attempt == 1:
                    continue
        if not quiet:
            console.print(f"[yellow]API error on /{path}: {last_error}[/yellow]")
        return None

    def cluster_names(self):
        """All cluster names known to the API, falling back to config.yaml."""
        data = self.get("clusters", quiet=True)
        names = []
        if isinstance(data, list):
            names = [c.get("name") for c in data if isinstance(c, dict) and c.get("name")]
        if not names:
            names = [c["name"] for c in self.overrides.values()]
        return names

    def resolve_clusters(self, requested=None):
        """Selected clusters, or every cluster when nothing was requested."""
        available = self.cluster_names()
        if not requested:
            if not available:
                console.print("[bold red]No cluster found on the API nor in config.yaml.[/bold red]")
                sys.exit(1)
            return available
        selected, unknown = [], []
        lowered = {n.lower(): n for n in available}
        for name in requested:
            real = lowered.get(name.lower())
            if real:
                selected.append(real)
            else:
                unknown.append(name)
        if unknown:
            console.print(f"[bold red]Unknown cluster(s): {', '.join(unknown)}.[/bold red] "
                          f"Available: {', '.join(available) or '(none)'}")
            sys.exit(1)
        return selected

    def cluster_servers(self, name):
        """Topology of one cluster, always a list."""
        return self.get(f"clusters/{name}/topology/servers", quiet=True) or []

    def cluster_definition(self, name, servers):
        """Expected topology of one cluster, config.yaml overrides applied."""
        return build_cluster_definition(name, servers, self.overrides.get(name.lower()))

    def collect_clusters(self, requested=None):
        """Discover clusters and their expected topology from the API.

        config.yaml no longer needs a 'clusters' section: names come from
        /api/clusters and hosts from each cluster topology. Entries still
        present in the file are kept as per-cluster overrides.
        """
        return [self.cluster_definition(name, self.cluster_servers(name))
                for name in self.resolve_clusters(requested)]


def add_cluster_argument(parser):
    parser.add_argument(
        "-c", "--cluster", action="append", default=[], metavar="NAME",
        help="Restrict to this cluster (repeatable). Default: ALL clusters.")


def parse_ts(value):
    try:
        return datetime.strptime(value, TS_FMT)
    except (TypeError, ValueError):
        return None
