# App Template Structure (Canonical)

This project uses a strict, uniform app template structure.

## Core rule

- `deployment.storages.*` defines **backing storage**.
- `deployment.paths` defines **container mount mappings**.

Volumes existing alone does not mount anything in the app container.

## Canonical path contract

For every `[[deployment.paths]]` entry:

- `name`: stable path identifier
- `level`: hierarchy level for deterministic ordering (`0` root, `1` child of root, etc.)
- `parentname`: parent path **name** (not docker path)
- `dockerpath`: container destination path
- `srctype`: one of `volume`, `git`, `s3`
- `srcname`: source object name for the selected `srctype`
- `srcpath`: source-relative path, use `"."` for source root

## Strict enforcement

- `parentname` must match an existing path `name`.
- `srcpath = "/"` is invalid. Use `srcpath = "."`.

## Legacy compatibility behavior

- Legacy templates are accepted only at load boundaries (local app configs, local template cache, fetched/shared templates, and seeded-template parsing).
- Legacy patterns are canonicalized in-memory first, then validated with strict deployment path resolution.
- Local writable copies are rewritten to canonical TOML only after validation succeeds.
- Invalid/ambiguous legacy templates are rejected (they are not silently accepted).

## Template lifecycle operations (Epic 10)

- Source templates (for example `shared/*`) are read-only.
- Editable templates are local working copies under `.templates/apps`.
- Create a local copy before editing source templates.
- Save operations enforce canonicalization and strict path validation.
- Reset/apply operations support impact preview before apply.

## Converter for old templates

Use the converter to migrate old templates to canonical form:

```bash
# Check templates and report non-canonical patterns
go run ./scripts/convert_app_template.go -in share/app/deployments -check

# Rewrite templates in place
go run ./scripts/convert_app_template.go -in share/app/deployments -write
```

The converter performs these updates:

- `parentname` pointing to a parent `dockerpath` → parent path `name`
- `srcpath = "/"` → `srcpath = "."`
- empty `srcpath` for source-backed paths → `srcpath = "."`

## Minimal multi-mount example

```toml
[[deployment.storages.volumes]]
name = "app-custom"
poolname = "tank"
volumedir = "app_custom"

[[deployment.storages.volumes]]
name = "app-documents"
poolname = "tank"
volumedir = "app_documents"

[[deployment.paths]]
name = "path-volume-app-custom"
level = 0
dockerpath = "/var/www/html/custom"
srctype = "volume"
srcname = "app-custom"
srcpath = "."

[[deployment.paths]]
name = "path-volume-app-documents"
level = 0
dockerpath = "/var/www/documents"
srctype = "volume"
srcname = "app-documents"
srcpath = "."
```

## Nested path example

```toml
[[deployment.paths]]
name = "path-root"
level = 0
dockerpath = "/usr/share/nginx/html"
srctype = "git"
srcname = "website-content"
srcpath = "."

[[deployment.paths]]
name = "path-pictures"
level = 1
parentname = "path-root"
dockerpath = "/usr/share/nginx/html/pictures"
srctype = "s3"
srcname = "pictures"
srcpath = "."
```
