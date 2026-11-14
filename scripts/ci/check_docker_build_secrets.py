#!/usr/bin/env python3
"""Credentials go to a Docker build as secrets, never as build arguments.

Secrets: RUN --mount=type=secret in the Dockerfile, secrets: in docker/build-push-action.
This check fails on:
  - an ARG or ENV in any Dockerfile whose name looks like a credential;
  - a build-args block in any workflow that reads a GitHub secret.
"""
import glob
import re
import sys

NAME = re.compile(r"(TOKEN|PASSWORD|PASSWD|SECRET|PRIVATE_KEY|API_KEY|CREDENTIAL)", re.I)
errors = []

for path in sorted(glob.glob("**/Dockerfile*", recursive=True)):
    if "/node_modules/" in path or path.startswith("share/"):
        continue
    with open(path, encoding="utf-8") as f:
        for n, line in enumerate(f, 1):
            m = re.match(r"\s*(ARG|ENV)\s+([A-Za-z0-9_]+)", line)
            if m and NAME.search(m.group(2)):
                errors.append(f"{path}:{n}: {m.group(1)} {m.group(2)} looks like a credential: use a build secret")

for path in sorted(glob.glob(".github/workflows/*.y*ml")):
    with open(path, encoding="utf-8") as f:
        lines = f.read().split("\n")
    for i, line in enumerate(lines):
        m = re.match(r"(\s*)build-args:", line)
        if not m:
            continue
        indent = len(m.group(1))
        j = i + 1
        while j < len(lines) and (not lines[j].strip() or len(lines[j]) - len(lines[j].lstrip()) > indent):
            if "secrets." in lines[j]:
                errors.append(f"{path}:{j + 1}: a build argument reads a GitHub secret: pass it in secrets: instead")
            j += 1

for e in errors:
    print(f"::error::{e}")
sys.exit(1 if errors else 0)
