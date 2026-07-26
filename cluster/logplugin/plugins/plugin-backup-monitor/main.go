// plugin-backup-monitor is the backup-paths automaton delivered as an external
// wire-v3 plugin. It has two halves that map onto two dedicated state machines:
//
//   - CONFIG half  → SeverityConfig findings → cluster ConfigStateMachine.
//     Backup-configuration coherence advisories: encryption off, physical
//     backup taken on the master, no off-site archive, split off (no partial
//     restore), and subscription-gated paths (free vs supported tier).
//
//   - MONITOR half → SeverityBackup findings → cluster BackupStateMachine.
//     Live backup observability: byte-progress, binlog-coverage watermark
//     (PITR window end), and backup validity/restorability.
//
// NOTE: this is a SKELETON. Several ClusterContext facts it reads are still
// roadmap-wired on the repman side (BackupType/BackupSplit/BackupLastMeta/
// BinlogWatermark/BackupBytesWritten) — the plugin already tolerates their zero
// values and lights up as those epic v3.2 items land. It does NOT produce
// backups; the bare plugin-backup / backup-engine-* namespace is reserved for a
// future backup-producing plugin class.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// planRank maps a cloud18 subscription plan to an ordinal so we can gate
// "supported-only" backup paths. free < pro < enterprise.
func planRank(plan string) int {
	switch plan {
	case "enterprise":
		return 2
	case "pro":
		return 1
	default: // "free" or unknown
		return 0
	}
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// enabled=false short-circuits (standard convention).
	if !wire.CfgBool(req.Config, "enabled", true) {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	cc := req.ClusterContext
	// Subscription plan: repman injects cloud18-subscription-plan into every
	// plugin's Config; allow a per-plugin override key too.
	plan := wire.CfgStr(req.Config, "subscription-plan",
		wire.CfgStr(req.Config, "cloud18-subscription-plan", "free"))
	rank := planRank(plan)
	// Minimum plan rank at which "supported-only" paths (off-site archive,
	// backup encryption, long-archived PITR, remote exec) are considered valid.
	supportedMin := planRank(wire.CfgStr(req.Config, "supported-paths-min-plan", "pro"))

	var resp wire.Response

	// ── CONFIG half — coherence advisories (SeverityConfig → ConfigStateMachine) ──

	if wire.CfgBool(req.Config, "require-encryption", true) && !cc.BackupEncrypted {
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "CONF0001",
			Severity:    "CONFIG",
			Description: fmt.Sprintf("Server %s: backups are not encrypted (no restic password / backup-crypt).", req.ServerURL),
			Remediations: []wire.Remediation{{
				Type: "repman_config", ConfigKey: "backup-restic-password", Risk: "safe",
				Description: "Set a restic repository password (or the future backup-crypt key) to encrypt backups.",
			}},
		})
	}

	if wire.CfgBool(req.Config, "warn-physical-on-master", true) &&
		cc.BackupType == "physical" && cc.BackupServerRole == "master" {
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "CONF0002",
			Severity:    "CONFIG",
			Description: fmt.Sprintf("Server %s: physical backup is taken on the master — prefer a backup slave to avoid load/locking.", req.ServerURL),
			Remediations: []wire.Remediation{{
				Type: "repman_config", ConfigKey: "db-servers-backup-hosts", Risk: "safe",
				Description: "Designate a prefered backup slave via db-servers-backup-hosts.",
			}},
		})
	}

	if wire.CfgBool(req.Config, "require-offsite", false) && !cc.BackupArchived {
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "CONF0003",
			Severity:    "CONFIG",
			Description: fmt.Sprintf("Server %s: no off-site archive configured (restic off) — backups are single-site.", req.ServerURL),
		})
	}

	if cc.BackupType == "logical" && !cc.BackupSplit {
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "CONF0004",
			Severity:    "CONFIG",
			Description: fmt.Sprintf("Server %s: logical backup is not split — partial (per-table) restore is unavailable.", req.ServerURL),
		})
	}

	// Subscription-gated paths: some backup paths are valid only for supported
	// subscribers. When the plan is below the supported threshold but a
	// supported-only path is configured, flag it as an invalid/unsupported path.
	if rank < supportedMin {
		if cc.BackupArchived {
			resp.Findings = append(resp.Findings, wire.Finding{
				ErrKey:   "CONF0005",
				Severity: "CONFIG",
				Description: fmt.Sprintf(
					"Server %s: off-site archive (restic) is a supported-tier backup path; current plan %q does not include it.",
					req.ServerURL, plan),
			})
		}
		if cc.BackupEncrypted {
			resp.Findings = append(resp.Findings, wire.Finding{
				ErrKey:   "CONF0006",
				Severity: "CONFIG",
				Description: fmt.Sprintf(
					"Server %s: encrypted backups are a supported-tier backup path; current plan %q does not include it.",
					req.ServerURL, plan),
			})
		}
	}

	// ── MONITOR half — backup observability (SeverityBackup → BackupStateMachine) ──

	if cc.BackupBytesWritten > 0 {
		// Numerator only — the % / rate needs an estimated total (roadmap, epic C).
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "BUP0001",
			Severity:    "BACKUP",
			Description: fmt.Sprintf("Server %s: backup in progress — %d bytes written.", req.ServerURL, cc.BackupBytesWritten),
			Count:       cc.BackupBytesWritten,
		})
	}

	if cc.BinlogWatermark != "" {
		// INFO watermark, not an open/close running state — binlog copies are too
		// frequent to open/close a state each cycle (epic C).
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "BUP0002",
			Severity:    "BACKUP",
			Description: fmt.Sprintf("Server %s: binlog backup available up to %s (PITR window end).", req.ServerURL, cc.BinlogWatermark),
		})
	}

	if cc.BackupLastMeta == "" {
		resp.Findings = append(resp.Findings, wire.Finding{
			ErrKey:      "BUP0003",
			Severity:    "BACKUP",
			Description: fmt.Sprintf("Server %s: no backup recorded yet — restorability unknown.", req.ServerURL),
		})
	}

	json.NewEncoder(os.Stdout).Encode(resp)
}
