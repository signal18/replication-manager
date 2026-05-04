// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_enterprise_compliance is a built-in plugin that reports when
// compliance modulesets have been refreshed by the back office via the
// git pull mechanism.
//
// The actual file sync and configurator reload happen in
// syncPluginDataFromPull() (server_git.go). This plugin only detects
// the change via CRC32 comparison and emits a security finding so the
// event is visible in the Security Logs tab.
//
// On free plan instances where no compliance files are pushed, it emits
// a persistent warning that the compliance is frozen at the build version.
package logplugin

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
)

func init() { Register(&EnterpriseCompliancePlugin{}) }

// EnterpriseCompliancePlugin implements LogPlugin.
type EnterpriseCompliancePlugin struct {
	mu          sync.Mutex
	lastDBCRC   uint32
	lastPrxCRC  uint32
	initialized bool
}

func (p *EnterpriseCompliancePlugin) Name() string { return "enterprise-compliance" }

func (p *EnterpriseCompliancePlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() || src.PluginDataDir == "" {
		return EvaluateResult{}
	}

	var findings []Finding

	dbFile := filepath.Join(src.PluginDataDir, "moduleset_mariadb.svc.mrm.db.json")
	prxFile := filepath.Join(src.PluginDataDir, "moduleset_mariadb.svc.mrm.proxy.json")

	dbCRC := fileCRC32(dbFile)
	prxCRC := fileCRC32(prxFile)

	p.mu.Lock()
	dbChanged := p.initialized && dbCRC != 0 && dbCRC != p.lastDBCRC
	prxChanged := p.initialized && prxCRC != 0 && prxCRC != p.lastPrxCRC
	if dbCRC != 0 {
		p.lastDBCRC = dbCRC
	}
	if prxCRC != 0 {
		p.lastPrxCRC = prxCRC
	}
	p.initialized = true
	p.mu.Unlock()

	if dbChanged || prxChanged {
		var parts string
		if dbChanged && prxChanged {
			parts = "database and proxy"
		} else if dbChanged {
			parts = "database"
		} else {
			parts = "proxy"
		}
		findings = append(findings, Finding{
			ErrKey:   "ENTCOMP001",
			Severity: SeveritySecurity,
			Description: fmt.Sprintf(
				"Server %s: compliance moduleset refreshed by back office (%s). "+
					"Updated compliance is now active for config generation.",
				src.ServerURL, parts),
		})
	}

	// Free plan warning — no compliance files pushed
	plan := ConfigStr(src.Config, "cloud18-subscription-plan", "")
	if (plan == "" || plan == "free") && dbCRC == 0 && prxCRC == 0 {
		findings = append(findings, Finding{
			ErrKey:   "ENTCOMPERR001",
			Severity: SeveritySecurity,
			Description: fmt.Sprintf(
				"Server %s: compliance modulesets are not refreshed on the free plan. "+
					"The configurator uses the version shipped with this build. "+
					"Upgrade to a support or partner plan to receive compliance updates.",
				src.ServerURL),
		})
	}

	return EvaluateResult{Findings: findings}
}

func fileCRC32(path string) uint32 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return crc32.ChecksumIEEE(data)
}
