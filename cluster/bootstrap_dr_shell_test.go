// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeWget answers every URL on host "dr" and fails every URL on host "main",
// logging each URL it was asked for.
const fakeWget = `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do case "$1" in -O|-qO) out="$2"; shift 2;; -O-) out="-"; shift;; -T|--header|--post-data) shift 2;; -*) shift;; *) url="$1"; shift;; esac; done
echo "$url" >> "$FAKE_LOG"
case "$url" in *//main*) exit 4;; esac
case "$url" in
 */bootstrap) printf 'echo "ran with $REPLICATION_MANAGER_URL"\n' > "$out";;
 */api/login) echo TOKEN;;
 *) [ -n "$out" ] && [ "$out" != "-" ] && : > "$out";;
esac
exit 0
`

// runWithFakeWget runs a shell snippet with the fake wget first in PATH and
// returns its output and the URLs requested.
func runWithFakeWget(t *testing.T, snippet string, env ...string) (string, []string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wget"), []byte(fakeWget), 0755); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(dir, "log")
	cmd := exec.Command(sh, "-c", snippet)
	cmd.Env = append([]string{"PATH=" + dir + ":" + os.Getenv("PATH"), "FAKE_LOG=" + logf}, env...)
	out, _ := cmd.CombinedOutput()
	b, _ := os.ReadFile(logf)
	return string(out), strings.Fields(string(b))
}

// shellBody extracts the script given to sh -c in an OpenSVC command.
func shellBody(t *testing.T, command string) string {
	t.Helper()
	m := regexp.MustCompile(`^-c '(.*)'$`).FindStringSubmatch(command)
	if m == nil {
		t.Fatalf("not a -c '...' command: %s", command)
	}
	return m[1]
}

func countPrefix(urls []string, prefix string) int {
	n := 0
	for _, u := range urls {
		if strings.HasPrefix(u, prefix) {
			n++
		}
	}
	return n
}

func TestBootstrapCommandsFallBackToDRWithFakeWget(t *testing.T) {
	main, dr := "https://main:10005", "https://dr:10005"
	cases := []struct {
		name, snippet string
		env           []string
	}{
		{"opensvc keys written by the active", shellBody(t, bootstrapInitCommand), []string{"REPLICATION_MANAGER_URL=" + main, "REPLICATION_MANAGER_URL_DR=" + main + " " + dr}},
		{"opensvc keys written by DR", shellBody(t, bootstrapInitCommand), []string{"REPLICATION_MANAGER_URL=" + main, "REPLICATION_MANAGER_URL_DR=" + dr + " " + main}},
		{"on-premise", onPremiseBootstrapCommand("repository/debian/mariadb"), []string{"REPLICATION_MANAGER_URL=" + main, "REPLICATION_MANAGER_URL_DR=" + main + " " + dr}},
	}
	for _, c := range cases {
		out, urls := runWithFakeWget(t, c.snippet, c.env...)
		if !strings.Contains(out, "ran with "+dr) {
			t.Errorf("%s: script not run from DR: %q", c.name, out)
		}
		if countPrefix(urls, main) != 1 {
			t.Errorf("%s: dead main URL tried %d times, want once: %v", c.name, countPrefix(urls, main), urls)
		}
	}

	// No DR key: fails on the main URL as before.
	out, _ := runWithFakeWget(t, shellBody(t, bootstrapInitCommand), "REPLICATION_MANAGER_URL="+main)
	if strings.Contains(out, "ran with") {
		t.Errorf("ran without any live URL: %q", out)
	}
}

// The OpenSVC bootstrap script's login loop keeps the URL that answered.
func TestOpenSVCBootstrapScriptLoginFallsBackWithFakeWget(t *testing.T) {
	b, err := os.ReadFile("../share/dashboard/static/configurator/opensvc/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)\nGET=.*?\ndone\n`).Find(b)
	if m == nil {
		t.Fatal("login loop not found in the bootstrap script")
	}
	out, urls := runWithFakeWget(t, string(m)+`echo "token=$TOKEN url=$REPLICATION_MANAGER_URL"`,
		"REPLICATION_MANAGER_URL=https://main:10005", "REPLICATION_MANAGER_URL_DR=https://main:10005 https://dr:10005")
	if !strings.Contains(out, "token=TOKEN url=https://dr:10005") {
		t.Fatalf("login did not fall back to DR: %q (urls %v)", out, urls)
	}
}

// Kubernetes: the generated init command picks the first URL answering /api/version.
func TestK8SInitPickWithFakeWget(t *testing.T) {
	cl := newTestCluster("k8stest")
	cl.Conf.ProvDbImg = "mariadb:10.11"
	cl.Conf.ApiServ = true
	cl.Conf.MonitorAddress = "main"
	cl.Conf.APIPort = "10005"
	cl.SetBootstrapDRURL("https://main:10005 https://dr:10005")
	full := cl.k8sDatabaseDeployment(&ServerMonitor{Name: "db1", Port: "3306"}, 3306, "n").Spec.Template.Spec.InitContainers[0].Command[2]
	m := regexp.MustCompile(`B=https\S* ; for u in .*? ; done ; `).FindString(full)
	if m == "" {
		t.Fatalf("pick loop not found: %s", full)
	}
	out, _ := runWithFakeWget(t, m+`echo "picked=$B"`)
	if !strings.Contains(out, "picked=https://dr:10005") {
		t.Fatalf("picked %q", out)
	}
}
