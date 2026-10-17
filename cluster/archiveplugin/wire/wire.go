// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package wire is the contract between replication-manager and its archive plugins (#2091):
// a program per archive kind (archiveplugin-plakar, …) that keeps a cluster's backups in an
// archive with its own engine, embedded in the plugin. replication-manager runs the plugin
// once per operation: the Request as one JSON document on its stdin, the archive's secrets in
// its environment (never on its command line nor in a file), and reads one Response JSON
// document on its stdout; progress lines go to its stderr. A plugin answers "--manifest" with
// its Manifest. Standard library only: the plugins are separate Go modules that import it.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// WireVersion is the version of this contract; a plugin built for another one is refused.
const WireVersion = 1

// BinaryPrefix names an archive plugin binary: archiveplugin-<plugin>. Not "plugin-": the
// log plugin loader takes every plugin-* it finds.
const BinaryPrefix = "archiveplugin-"

// ManifestFlag makes a plugin print its Manifest and exit.
const ManifestFlag = "--manifest"

// The environment of a plugin process: the archive's secrets, by the setting name of its
// manifest (EnvSecretPrefix + upper-cased name, "-" as "_"), and the S3 keys of the
// archive's provider, short-lived ones with a session token for the Cloud18 SSO.
const (
	EnvWireVersion    = "REPMAN_ARCHIVEPLUGIN_WIRE_VERSION"
	EnvSecretPrefix   = "REPMAN_ARCHIVEPLUGIN_SECRET_"
	EnvS3AccessKey    = "REPMAN_ARCHIVEPLUGIN_S3_ACCESS_KEY"
	EnvS3SecretKey    = "REPMAN_ARCHIVEPLUGIN_S3_SECRET_KEY"
	EnvS3SessionToken = "REPMAN_ARCHIVEPLUGIN_S3_SESSION_TOKEN"
)

// The operations of an archive plugin.
const (
	OpProbe   = "probe"   // open the archive, or create it when missing and allowed
	OpPush    = "push"    // archive a local folder: Response.Snapshot
	OpList    = "list"    // the snapshots: Response.Snapshots
	OpRestore = "restore" // fetch a snapshot (or one path of it) into a local folder
	OpForget  = "forget"  // delete one snapshot
	OpPurge   = "purge"   // apply the cluster's retention policy
	OpSize    = "size"    // the bytes the archive takes: Response.Size
	OpRemove  = "remove"  // delete the archive and all its content
	OpSync    = "sync"    // copy snapshots into another archive (Request.Target)
)

// Setting is one setting of a plugin's manifest, as replication-manager's archive manifest
// types it (string, int, bool, enum, secret) and groups it (main, purge, advanced).
type Setting struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
	Group  string   `json:"group,omitempty"`
}

// Manifest is what a plugin answers to ManifestFlag: its archive plugin manifest.
type Manifest struct {
	WireVersion  int       `json:"wireVersion"`
	Name         string    `json:"name"`        // the archive plugin name, archive-<plugin>-<kind>
	Description  string    `json:"description"` // what it stores into: Local folder, S3 bucket…
	Tier         string    `json:"tier,omitempty"`
	Settings     []Setting `json:"settings"`
	Capabilities []string  `json:"capabilities"`
}

// Store is the archive an operation runs on: its plugin, its settings (secrets excluded,
// they are in the environment) and, for an S3 archive, its provider's endpoint and region.
type Store struct {
	Plugin   string            `json:"plugin"`
	Name     string            `json:"name"`
	Settings map[string]string `json:"settings"`
	Endpoint string            `json:"endpoint,omitempty"`
	Region   string            `json:"region,omitempty"`
}

// Policy is the cluster's retention policy (backup-keep-*): a zero count or an empty duration
// is not part of it.
type Policy struct {
	KeepLast          int    `json:"keepLast,omitempty"`
	KeepHourly        int    `json:"keepHourly,omitempty"`
	KeepDaily         int    `json:"keepDaily,omitempty"`
	KeepWeekly        int    `json:"keepWeekly,omitempty"`
	KeepMonthly       int    `json:"keepMonthly,omitempty"`
	KeepYearly        int    `json:"keepYearly,omitempty"`
	KeepWithin        string `json:"keepWithin,omitempty"`
	KeepWithinHourly  string `json:"keepWithinHourly,omitempty"`
	KeepWithinDaily   string `json:"keepWithinDaily,omitempty"`
	KeepWithinWeekly  string `json:"keepWithinWeekly,omitempty"`
	KeepWithinMonthly string `json:"keepWithinMonthly,omitempty"`
	KeepWithinYearly  string `json:"keepWithinYearly,omitempty"`
}

// Request is one operation.
type Request struct {
	WireVersion int    `json:"wireVersion"`
	Op          string `json:"op"`
	Store       Store  `json:"store"`
	Create      bool   `json:"create,omitempty"` // probe: create the archive when missing
	// push: the folder to archive and the snapshot's tags and host
	Path string   `json:"path,omitempty"`
	Tags []string `json:"tags,omitempty"`
	Host string   `json:"host,omitempty"`
	// restore, forget: the snapshot; restore: the path in it ("" for all) and the folder
	Snapshot string `json:"snapshot,omitempty"`
	Include  string `json:"include,omitempty"`
	Target   string `json:"target,omitempty"`
	// purge: the policy; DryRun lists what would go
	Policy *Policy `json:"policy,omitempty"`
	DryRun bool    `json:"dryRun,omitempty"`
	// sync: the archive the snapshots go to (its secrets in the environment with the
	// EnvSecretPrefix + "TARGET_" prefix), and the snapshots ("" for all)
	SyncTo    *Store   `json:"syncTo,omitempty"`
	Snapshots []string `json:"snapshots,omitempty"`
}

// Snapshot is one snapshot of an archive.
type Snapshot struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Host  string    `json:"host,omitempty"`
	Tags  []string  `json:"tags,omitempty"`
	Paths []string  `json:"paths,omitempty"`
	Size  int64     `json:"size,omitempty"`
}

// Response is the outcome of one operation; Error is set when it failed.
type Response struct {
	WireVersion int        `json:"wireVersion"`
	Error       string     `json:"error,omitempty"`
	Created     bool       `json:"created,omitempty"`  // probe created the archive
	Location    string     `json:"location,omitempty"` // probe: where the archive is
	Snapshot    *Snapshot  `json:"snapshot,omitempty"` // push
	Snapshots   []Snapshot `json:"snapshots,omitempty"`
	Removed     []string   `json:"removed,omitempty"` // purge, forget
	Size        int64      `json:"size,omitempty"`
	Files       int64      `json:"files,omitempty"`
}

// ReadRequest reads the request a plugin gets on its stdin and checks its wire version.
func ReadRequest(r io.Reader) (Request, error) {
	var req Request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return req, fmt.Errorf("archive plugin request: %w", err)
	}
	if req.WireVersion != WireVersion {
		return req, fmt.Errorf("archive plugin request: wire version %d, this plugin speaks %d", req.WireVersion, WireVersion)
	}
	if req.Op == "" {
		return req, errors.New("archive plugin request: no op")
	}
	return req, nil
}

// WriteRequest writes the request replication-manager sends on a plugin's stdin.
func WriteRequest(w io.Writer, req Request) error {
	req.WireVersion = WireVersion
	return json.NewEncoder(w).Encode(req)
}

// WriteResponse writes the plugin's response on its stdout, err as its Error.
func WriteResponse(w io.Writer, resp Response, err error) error {
	resp.WireVersion = WireVersion
	if err != nil {
		resp.Error = err.Error()
	}
	return json.NewEncoder(w).Encode(resp)
}

// ReadResponse reads a plugin's response and turns its Error into an error.
func ReadResponse(r io.Reader) (Response, error) {
	var resp Response
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return resp, fmt.Errorf("archive plugin response: %w", err)
	}
	if resp.WireVersion != WireVersion {
		return resp, fmt.Errorf("archive plugin response: wire version %d, replication-manager speaks %d", resp.WireVersion, WireVersion)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// SecretEnv is the environment variable of the archive secret name.
func SecretEnv(name string) string {
	out := []byte(EnvSecretPrefix)
	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c-'a'+'A')
		case c == '-':
			out = append(out, '_')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
