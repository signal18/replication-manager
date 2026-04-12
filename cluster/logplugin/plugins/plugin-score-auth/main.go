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

	// isOn handles both ON/OFF and 1/0 representations of boolean variables.
	// MariaDB's information_schema.global_variables can return "1" instead of "ON"
	// for some boolean variables depending on version and variable type.
	isOn := func(key string) bool {
		val := strings.ToUpper(strings.TrimSpace(get(key)))
		return val == "ON" || val == "1"
	}

	// HasStrongPwd: password validation plugin is active and enforced.
	//
	// MySQL: validate_password plugin (5.x) or component (8.0+) must be loaded.
	//        Detected via presence of validate_password_policy (plugin, underscore)
	//        or validate_password.policy (component, dot).
	// MariaDB: strict_password_validation=ON AND at least one complexity plugin
	//          (simple_password_check OR cracklib_password_check) must be loaded.
	//          Plugin presence is detected via characteristic system variables that
	//          only appear in SHOW GLOBAL VARIABLES when the plugin is loaded.
	//          mysql_native_password accounts are intentionally NOT penalised here —
	//          auth plugin strength is a separate concern from password policy enforcement.
	var validatePwd bool
	if req.ServerVersion.Flavor == "MariaDB" {
		_, hasSimple   := v["simple_password_check_minimal_length"]
		_, hasCracklib := v["cracklib_password_check_dictionary"]
		validatePwd = isOn("strict_password_validation") && (hasSimple || hasCracklib)
	} else {
		// MySQL 5.x plugin: exposes validate_password_policy (underscore)
		// MySQL 8.0+ component: exposes validate_password.policy (dot)
		_, hasPlugin    := v["validate_password_policy"]
		_, hasComponent := v["validate_password.policy"]
		validatePwd = hasPlugin || hasComponent
	}

	// HasStrongPwd for MariaDB is purely about the password validation plugin.
	// For MySQL we also require that no non-locked account uses a legacy weak-auth
	// plugin, since validate_password + caching_sha2 are designed to go together.
	var hasStrongPwd bool
	if req.ServerVersion.Flavor == "MariaDB" {
		hasStrongPwd = validatePwd
	} else {
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
		hasStrongPwd = validatePwd && !weakFound
	}

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
			Detail: fmt.Sprintf("validate_password=%s strict_password_validation=%s", get("validate_password"), get("strict_password_validation"))},
		{Tag: "HasParsecPlugins", Pass: hasParsec, Detail: "parsec plugin user found"},
		{Tag: "HasPasswordRotation", Pass: hasRotation,
			Detail: fmt.Sprintf("default_password_lifetime=%d", lifetime)},
		{Tag: "HasPrepareStatement", Pass: hasPrepare,
			Detail: fmt.Sprintf("max_prepared_stmt_count=%d", maxPS)},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
