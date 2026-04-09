// plugin-score-passwords evaluates cleartext-password exposure checks:
//
//	NoClearPwdBinlogs — no cleartext passwords detected in recent binlog events
//	NoClearPwdConfigs — no cleartext passwords detected in DB server config files
//	                    (result set by dbjob SSH script scanning my.cnf/.my.cnf)
//	NoClearPwdHistory — no cleartext passwords detected in shell/mysql history
//	                    (result set by dbjob SSH script scanning .bash_history/.mysql_history)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// Patterns that suggest a cleartext password in a SQL statement.
var clearPwdPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)IDENTIFIED\s+BY\s+'[^']+'`),
	regexp.MustCompile(`(?i)PASSWORD\s*=\s*'[^']+'`),
	regexp.MustCompile(`(?i)SET\s+PASSWORD\s*=\s*'[^']+'`),
}

func hasClearPwd(text string) bool {
	for _, re := range clearPwdPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// NoClearPwdBinlogs: scan recent binlog events
	binlogClean := true
	var binlogDetail string
	for _, ev := range req.BinlogEvents {
		if hasClearPwd(ev.Query) {
			binlogClean = false
			binlogDetail = fmt.Sprintf("cleartext password pattern found in binlog event at %s", ev.Timestamp)
			break
		}
	}

	// NoClearPwdConfigs / NoClearPwdHistory: results come from dbjob SSH scripts
	// stored in ClusterContext. Plugin just passes them through.
	noClearConfig := !req.ClusterContext.ConfigClearPwd
	noClearHistory := !req.ClusterContext.HistoryClearPwd

	configDetail := "no cleartext passwords found in DB server config files (dbjob result)"
	if !noClearConfig {
		configDetail = "cleartext password detected in DB server config file (dbjob result)"
	}
	historyDetail := "no cleartext passwords found in shell/mysql history (dbjob result)"
	if !noClearHistory {
		historyDetail = "cleartext password detected in shell/mysql history (dbjob result)"
	}

	checks := []wire.ScoreCheck{
		{Tag: "NoClearPwdBinlogs", Pass: binlogClean, Detail: binlogDetail},
		{Tag: "NoClearPwdConfigs", Pass: noClearConfig, Detail: configDetail},
		{Tag: "NoClearPwdHistory", Pass: noClearHistory, Detail: historyDetail},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
