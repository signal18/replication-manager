package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

func TestDeployImageRule(t *testing.T) {
	cl := &Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:latest", ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	if cl.deployImage() != "mariadb:latest" {
		t.Fatalf("unresolved pointer renders as declared: %s", cl.deployImage())
	}
	cl.Conf.ProvDbImgResolved = "mariadb:latest=mariadb:13.0.2"
	if cl.deployImage() != "mariadb:13.0.2" {
		t.Fatalf("resolved pointer renders the release: %s", cl.deployImage())
	}
	cl.Conf.ProvDbImgResolved = "mariadb:11.8=mariadb:11.8.9" // a record of another declaration is ignored
	if cl.deployImage() != "mariadb:latest" {
		t.Fatalf("stale record must be ignored: %s", cl.deployImage())
	}
	if err := cl.SetProvDBImage("mariadb:11.4.5"); err != nil || cl.Conf.ProvDbImgResolved != "" {
		t.Fatalf("a new declaration drops the record: err=%v record=%q", err, cl.Conf.ProvDbImgResolved)
	}
}

func TestResolveDatabaseImage(t *testing.T) {
	calls := 0
	loadImageCatalog = func(c *Cluster) (*releases.Catalog, error) {
		calls++
		return &releases.Catalog{Table: releases.Table{LTS: map[string][]string{"mariadb": {"11.8"}}},
			Tags: map[string][]releases.Tag{"mariadb": {{Name: "13.0.2"}, {Name: "11.8.9"}, {Name: "11.8.8"}}}, Source: "test"}, nil
	}
	defer func() { loadImageCatalog = nil }()
	cl := &Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:latest", ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	if err := cl.ResolveDatabaseImage(false); err != nil || cl.Conf.ProvDbImgResolved != "mariadb:latest=mariadb:13.0.2" {
		t.Fatalf("first resolution: err=%v record=%q", err, cl.Conf.ProvDbImgResolved)
	}
	cl.ResolveDatabaseImage(false)
	if calls != 1 {
		t.Fatalf("a valid record is kept without force, calls=%d", calls)
	}
	cl.ResolveDatabaseImage(true)
	if calls != 2 {
		t.Fatalf("force resolves again, calls=%d", calls)
	}
	cl.Conf.ProvDbImg = "mariadb:11.8"
	if err := cl.ResolveDatabaseImage(true); err != nil || cl.deployImage() != "mariadb:11.8.9" {
		t.Fatalf("a line resolves to its newest release: err=%v image=%s", err, cl.deployImage())
	}
	cl.Conf.ProvDbImg = "mariadb:11.8.9"
	if err := cl.ResolveDatabaseImage(true); err != nil || cl.Conf.ProvDbImgResolved != "" {
		t.Fatalf("an explicit declaration needs no record: err=%v record=%q", err, cl.Conf.ProvDbImgResolved)
	}
	cl.Conf.ProvDbImg = "mariadb:12.9"
	if err := cl.ResolveDatabaseImage(true); err != nil || cl.deployImage() != "mariadb:12.9" || cl.Conf.ProvDbImgResolved != "" {
		t.Fatalf("a line absent from the list comes back unchanged, no error, no record: err=%v image=%s record=%q", err, cl.deployImage(), cl.Conf.ProvDbImgResolved)
	}
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	n := calls
	if err := cl.ResolveDatabaseImage(true); err != nil || calls != n {
		t.Fatalf("on-premise never resolves: err=%v", err)
	}
}

func TestPlanRollingUpgradeFromEmbeddedList(t *testing.T) {
	loadImageCatalog = func(c *Cluster) (*releases.Catalog, error) {
		return &releases.Catalog{Table: releases.Table{LTS: map[string][]string{"mariadb": {"11.4", "11.8", "12.3"}}},
			Tags: map[string][]releases.Tag{"mariadb": {{Name: "12.3.2"}, {Name: "12.0.2"}, {Name: "11.8.8"}, {Name: "11.5.2"}, {Name: "11.4.9"}, {Name: "11.4.8"}}}, Source: "test"}, nil
	}
	defer func() { loadImageCatalog = nil }()
	cl := &Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:11.4", ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	for target, want := range map[string][2]string{"patch": {"mariadb:11.4.9", "mariadb:11.4"}, "next-minor": {"mariadb:11.5.2", "mariadb:11.5"}, "next-lts": {"mariadb:11.8.8", "mariadb:11.8"}, "next-major": {"mariadb:12.0.2", "mariadb:12.0"}, "last-lts": {"mariadb:12.3.2", "mariadb:12.3"}} {
		p, err := cl.PlanRollingUpgrade(target, "")
		if err != nil || p.TargetImage != want[0] || p.DeclaredAfter != want[1] {
			t.Fatalf("%s: err=%v target=%v declaredAfter=%v", target, err, p, want)
		}
	}
	p, err := cl.PlanRollingUpgrade("version", "11.8.8")
	if err != nil || p.TargetImage != "mariadb:11.8.8" || p.DeclaredAfter != "mariadb:11.8.8" {
		t.Fatalf("version 11.8.8: err=%v plan=%+v", err, p)
	}
	if _, err := cl.PlanRollingUpgrade("version", "10.11"); err == nil {
		t.Fatal("downgrade must be refused")
	}
	p, err = cl.PlanRollingUpgrade("version", "11.8.7")
	if err != nil || p.TargetImage != "mariadb:11.8.7" || !strings.Contains(strings.Join(p.Warnings, " "), "not in the") {
		t.Fatalf("a given release absent from the list is taken as is with a warning: err=%v plan=%+v", err, p)
	}
	cl.Conf.ProvDbImg = "mariadb:12.9"
	cl.Servers = nil
	p, err = cl.PlanRollingUpgrade("patch", "")
	if err != nil || p.TargetImage != "mariadb:12.9" {
		t.Fatalf("patch on a line absent from the list keeps the declared tag: err=%v plan=%+v", err, p)
	}
	cl.Conf.ProvDbImg = "mariadb:12.3" // declared one major higher: the default upgrade follows the declaration
	p, err = cl.PlanRollingUpgrade("", "")
	if err != nil || p.TargetImage != "mariadb:12.3.2" || p.DeclaredAfter != "mariadb:12.3" {
		t.Fatalf("default upgrade follows the declared line: err=%v plan=%+v", err, p)
	}
	cl.Conf.ProvDbImg = "mariadb:11.4"
	if _, err := cl.PlanRollingUpgrade("sideways", ""); err == nil {
		t.Fatal("unknown target must fail")
	}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{"prov-db-docker-img": "mariadb:11.4"}
	p, _ = cl.PlanRollingUpgrade("next-lts", "")
	if !strings.Contains(strings.Join(p.Warnings, " "), "pinned") {
		t.Fatalf("pinned image not reported: %v", p.Warnings)
	}
	if _, err := cl.PrepareRollingUpgrade("next-lts", ""); err == nil {
		t.Fatal("prepare must refuse to move a pinned image")
	}
}
