// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestAPIUserRotatePassword is the real-cluster regtest of a user's password rotation (the
// ban of a compromised user), through repman's own API:
//
//  1. a cluster user is added with a known password, which repman accepts;
//  2. its password is rotated: the answer carries a new 24-character password, once;
//  3. repman refuses the old password and accepts the new one;
//  4. the user is dropped.
//
// The instance's list (api-credentials, shared by every cluster) is covered by the unit tests:
// rotating it here would change the logins of the test environment.
func (regtest *RegTest) TestAPIUserRotatePassword(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const user = "rt-rotate-user"
	const first = "Rt-first-Pw-2026"
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST api-user-rotate-password: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}
	if APIToken == nil || APIRequestBody == nil {
		return fail("the server hooks are not wired")
	}
	token, err := APIToken(cl, config.GrantGrantAdd, config.GrantGrantDrop, config.GrantClusterRotatePasswords)
	if err != nil {
		return fail("no API user with the user add, drop and rotation grants: %s", err)
	}
	post := func(path string, body interface{}) (int, []byte) {
		b, _ := json.Marshal(body)
		code, _, resp, err := APIRequestBody(token, http.MethodPost, "/api/clusters/"+cl.Name+path, b)
		if err != nil {
			return 0, []byte(err.Error())
		}
		return code, resp
	}
	// the accounts as repman checks a password (a Cloud18-only login page refuses every local
	// account but the admin ones, so the HTTP login cannot tell)
	logsIn := func(pass string) bool {
		_, err := cl.GetAPIUser(user, pass)
		return err == nil
	}
	defer post("/users/drop", map[string]string{"username": user})

	if code, resp := post("/users/add", map[string]string{"username": user, "password": first, "grants": "show", "roles": "visitor"}); code != http.StatusOK {
		return fail("adding %s (%d): %s", user, code, strings.TrimSpace(string(resp)))
	}
	if !logsIn(first) {
		return fail("%s does not log in with its first password", user)
	}
	code, resp := post("/users/rotate-password", map[string]string{"username": user})
	if code != http.StatusOK {
		return fail("the rotation was refused (%d): %s", code, strings.TrimSpace(string(resp)))
	}
	var res struct {
		Password string `json:"password"`
		Scope    string `json:"scope"`
	}
	if err := json.Unmarshal(resp, &res); err != nil || len(res.Password) != 24 {
		return fail("the answer carries no 24-character password (scope %q, length %d)", res.Scope, len(res.Password))
	}
	if logsIn(first) {
		return fail("the old password still logs in")
	}
	if !logsIn(res.Password) {
		return fail("the new password does not log in")
	}
	logf(config.LvlInfo, "PASS (%s list)", res.Scope)
	return true
}
