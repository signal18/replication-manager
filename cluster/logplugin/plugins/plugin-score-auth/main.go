// plugin-score-auth evaluates authentication-related compliance checks:
//
//	NoEmptyPassword    — no unlocked account has an empty password
//	HasStrongPwd       — password validation plugin is active AND no weak-auth accounts
//	HasParsecPlugins   — at least one account uses the PARSEC plugin (MariaDB 11.6+)
//	HasPasswordRotation — default_password_lifetime > 0 (passwords expire)
//	HasPrepareStatement — server is configured to support prepared statements
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

var socketPlugins = map[string]bool{
	"unix_socket": true, "auth_socket": true, "gssapi": true,
	"authentication_pam": true, "auth_pam": true,
}

var strongPlugins = map[string]bool{
	"ed25519": true, "caching_sha2_password": true,
	"authentication_fido": true, "parsec": true,
	"auth_gssapi": true, "gssapi": true,
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	v := req.ServerVariables
	get := func(key string) string { return strings.TrimSpace(v[key]) }

	// NoEmptyPassword: no unlocked non-socket account has empty auth string
	noEmpty := true
	var emptyDetail string
	for _, u := range req.DatabaseUsers {
		if u.AccountLocked || socketPlugins[strings.ToLower(u.Plugin)] {
			continue
		}
		if u.PasswordEmpty {
			noEmpty = false
			emptyDetail = fmt.Sprintf("account '%s'@'%s' has no password", u.User, u.Host)
			break
		}
	}

	// HasStrongPwd: password validation plugin is active and enforced.
	//
	// MySQL: validate_password / validate_password_policy must be active.
	// MariaDB: strict_password_validation=ON AND at least one complexity plugin
	//          (simple_password_check OR cracklib_password_check) must be loaded.
	//          The characteristic variable for each plugin is present in SHOW GLOBAL
	//          VARIABLES only when the plugin is loaded.
	var validatePwd bool
	if req.ServerVersion.Flavor == "MariaDB" {
		strictOn := strings.ToUpper(get("strict_password_validation")) == "ON"
		_, hasSimple   := v["simple_password_check_minimal_length"]
		_, hasCracklib := v["cracklib_password_check_dictionary"]
		validatePwd = strictOn && (hasSimple || hasCracklib)
	} else {
		// MySQL 5.x plugin: exposes validate_password_policy (underscore)
		// MySQL 8.0+ component: exposes validate_password.policy (dot)
		// Both are absent when the plugin/component is not loaded.
		_, hasPlugin    := v["validate_password_policy"]
		_, hasComponent := v["validate_password.policy"]
		validatePwd = hasPlugin || hasComponent
	}
	// Also fail if any non-locked account uses a weak plugin
	weakFound := false
	for _, u := range req.DatabaseUsers {
		if u.AccountLocked || socketPlugins[strings.ToLower(u.Plugin)] {
			continue
		}
		if !strongPlugins[strings.ToLower(u.Plugin)] {
			weakFound = true
			break
		}
	}
	hasStrongPwd := validatePwd && !weakFound

	// HasParsecPlugins: at least one account uses parsec
	hasParsec := false
	for _, u := range req.DatabaseUsers {
		if strings.ToLower(u.Plugin) == "parsec" {
			hasParsec = true
			break
		}
	}

	// HasPasswordRotation: default_password_lifetime > 0
	lifetime, _ := strconv.Atoi(get("default_password_lifetime"))
	hasRotation := lifetime > 0

	// HasPrepareStatement: max_prepared_stmt_count > 0 (server supports prepared stmts)
	maxPS, _ := strconv.Atoi(get("max_prepared_stmt_count"))
	hasPrepare := maxPS > 0

	checks := []wire.ScoreCheck{
		{Tag: "NoEmptyPassword", Pass: noEmpty, Detail: emptyDetail},
		{Tag: "HasStrongPwd", Pass: hasStrongPwd,
			Detail: fmt.Sprintf("validate_password=%s weak_accounts=%v", get("validate_password"), weakFound)},
		{Tag: "HasParsecPlugins", Pass: hasParsec, Detail: "parsec plugin user found"},
		{Tag: "HasPasswordRotation", Pass: hasRotation,
			Detail: fmt.Sprintf("default_password_lifetime=%d", lifetime)},
		{Tag: "HasPrepareStatement", Pass: hasPrepare,
			Detail: fmt.Sprintf("max_prepared_stmt_count=%d", maxPS)},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
