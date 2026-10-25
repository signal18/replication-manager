// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #2159: the subscription is what the instance asked for (cloud18-subscription-plan) and what the
// back office answered in the pull repository's cloud18.toml; until it answers, pending.
func TestCloud18SubscriptionFromPull(t *testing.T) {
	rm := registerRepman(t, "https://identity.example/realms/cloud18", []byte("0123456789abcdef"))
	rm.Conf.Cloud18, rm.Conf.Cloud18Domain, rm.Conf.Cloud18SubDomain, rm.Conf.Cloud18SubDomainZone = true, "acme", "ovh", "fr-1"
	rm.Conf.Cloud18GitUser, rm.Conf.Cloud18SubscriptionPlan = "admin@acme.example", "support"

	rm.readCloud18PullState()
	if v := rm.cloud18SubscriptionView(); v["state"] != "pending" || v["plan"] != "support" || v["planActive"] != "" || v["email"] != "admin@acme.example" {
		t.Fatalf("no answer yet: %v", v)
	}

	pull := filepath.Join(rm.Conf.WorkingDir, ".pull")
	os.MkdirAll(pull, 0o755)
	os.WriteFile(filepath.Join(pull, "cloud18.toml"), []byte("[default]\ncloud18-subscription-plan-active = \"free\"\ncloud18-subscription-state = \"refused\"\ncloud18-subscription-message = \"support needs a contract\"\n"), 0o644)
	rm.readCloud18PullState()
	if v := rm.cloud18SubscriptionView(); v["planActive"] != "free" || v["state"] != "refused" || v["message"] != "support needs a contract" {
		t.Fatalf("a refused plan keeps the previous one active: %v", v)
	}
	os.WriteFile(filepath.Join(pull, "cloud18.toml"), []byte("cloud18-subscription-plan-active = \"free\"\ncloud18-subscription-state = \"active\"\n"), 0o644)
	rm.readCloud18PullState()
	if v := rm.cloud18SubscriptionView(); v["state"] != "pending" {
		t.Fatalf("the back office has not handled the request yet: %v", v)
	}
	os.WriteFile(filepath.Join(pull, "cloud18.toml"), []byte("cloud18-subscription-plan-active = \"support\"\ncloud18-subscription-state = \"active\"\n"), 0o644)
	rm.readCloud18PullState()
	if v := rm.cloud18SubscriptionView(); v["planActive"] != "support" || v["state"] != "active" {
		t.Fatalf("the plan applied: %v", v)
	}
}

// gitRepoWithOrigin is a working directory cloned from a local bare repository, one commit pushed.
func gitRepoWithOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	for _, args := range [][]string{
		{"init", "-q", "--bare", "-b", "master", bare},
		{"clone", "-q", bare, work},
		{"-C", work, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "first"},
		{"-C", work, "push", "-q", "origin", "HEAD:master"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	return work
}

// #2159 review: the unregistration stops Cloud18 only once GitLab has the request (else the back
// office would never see it), and writes cloud18 = false where the value lives (a config file's
// value is immutable) so a restart keeps it off.
func TestCloud18RequestUnregister(t *testing.T) {
	for _, immutable := range []bool{false, true} {
		rm := registerRepman(t, "https://identity.example/realms/cloud18", []byte("0123456789abcdef"))
		rm.Conf.WorkingDir = gitRepoWithOrigin(t)
		rm.Conf.Cloud18, rm.Conf.Cloud18Domain, rm.Conf.Cloud18SubDomain, rm.Conf.Cloud18SubDomainZone = true, "acme", "ovh", "fr-1"
		if immutable {
			rm.Conf.SetImmuableFlag("cloud18", true)
		}
		out, err := rm.cloud18RequestUnregister()
		if err != nil || out["state"] != "requested" || rm.Conf.Cloud18 || !rm.Conf.Cloud18Unregister {
			t.Fatalf("immutable %v: pushed request: %v %v, cloud18 %v", immutable, out, err, rm.Conf.Cloud18)
		}
		if v, ok := rm.Conf.GetImmuableFlag("cloud18"); immutable && (!ok || v != false) {
			t.Fatalf("the config file's value must become false: %v %v", v, ok)
		}
		if v, ok := rm.Conf.GetDynamicFlag("cloud18"); !immutable && (!ok || v != false) {
			t.Fatalf("the dynamic value must become false: %v %v", v, ok)
		}
		if _, err := rm.cloud18RequestUnregister(); err != errCloud18NotRegistered {
			t.Fatalf("a second unregister: %v", err)
		}
	}

	// a commit GitLab does not have yet (the push failed): Cloud18 stays on
	rm := registerRepman(t, "https://identity.example/realms/cloud18", []byte("0123456789abcdef"))
	rm.Conf.WorkingDir = gitRepoWithOrigin(t)
	if out, err := exec.Command("git", "-C", rm.Conf.WorkingDir, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "not pushed").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	rm.Conf.Cloud18, rm.Conf.Cloud18Domain, rm.Conf.Cloud18SubDomain, rm.Conf.Cloud18SubDomainZone = true, "acme", "ovh", "fr-1"
	if _, err := rm.cloud18RequestUnregister(); err != errCloud18UnregisterNotPushed || !rm.Conf.Cloud18 {
		t.Fatalf("not pushed: %v, cloud18 %v", err, rm.Conf.Cloud18)
	}
}

// #2159: a plan request is validated, recorded as the requested plan, and pending until the back
// office answers.
func TestCloud18RequestPlan(t *testing.T) {
	rm := registerRepman(t, "https://identity.example/realms/cloud18", []byte("0123456789abcdef"))
	if _, err := rm.cloud18RequestPlan("support"); err != errCloud18NotRegistered {
		t.Fatalf("not registered: %v", err)
	}
	rm.Conf.Cloud18 = true
	if _, err := rm.cloud18RequestPlan("platinum"); err == nil {
		t.Fatal("an unknown plan must be refused")
	}
	view, err := rm.cloud18RequestPlan(" Support ")
	if err != nil || rm.Conf.Cloud18SubscriptionPlan != "support" || view["state"] != "pending" {
		t.Fatalf("requested: %v %v plan %q", view, err, rm.Conf.Cloud18SubscriptionPlan)
	}
	if v, _ := rm.Conf.GetDynamicFlag("cloud18-subscription-plan"); v != "support" {
		t.Fatalf("the request must be saved for the push: %v", v)
	}
}
