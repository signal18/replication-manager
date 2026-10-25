// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"os"
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
