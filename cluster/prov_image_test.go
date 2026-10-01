package cluster

import (
	"context"
	"errors"
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
	resolveTag = func(ctx context.Context, repo, tag string) (string, string, bool, error) {
		calls++
		switch tag {
		case "latest":
			return "13.0.2", "sha256:aaa", true, nil
		case "broken":
			return "", "", true, errors.New("registry down")
		}
		return tag, "", true, nil
	}
	defer func() { resolveTag = releases.ResolveTag }()
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
	cl.Conf.ProvDbImg = "mariadb:11.8.9"
	if err := cl.ResolveDatabaseImage(true); err != nil || cl.Conf.ProvDbImgResolved != "" || calls != 2 {
		t.Fatalf("an explicit declaration needs no record: err=%v record=%q calls=%d", err, cl.Conf.ProvDbImgResolved, calls)
	}
	cl.Conf.ProvDbImg = "mariadb:broken"
	if err := cl.ResolveDatabaseImage(true); err == nil || cl.deployImage() != "mariadb:broken" {
		t.Fatalf("failure keeps the declared name: err=%v image=%s", err, cl.deployImage())
	}
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	cl.Conf.ProvDbImg = "mariadb:latest"
	if err := cl.ResolveDatabaseImage(true); err != nil || calls != 3 {
		t.Fatalf("on-premise never resolves: err=%v calls=%d", err, calls)
	}
}
