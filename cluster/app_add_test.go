package cluster

import (
	"hash/crc64"
	"path/filepath"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newTestClusterForAddApp(t *testing.T, name string) *Cluster {
	t.Helper()
	root := t.TempDir()
	return &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir:             root,
			Cloud18Domain:          "example",
			Cloud18SubDomain:       "sub",
			Cloud18SubDomainZone:   "zone",
			ProvOrchestratorEnable: "",
		},
		crcTable: crc64.MakeTable(crc64.ISO),
	}
}

func newTestAppForAddApp(name, port, agents string) *App {
	return &App{
		Name:  name,
		Host:  name,
		Port:  port,
		Mutex: &sync.Mutex{},
		AppConfig: &config.AppConfig{
			AppHost:       name,
			AppPort:       port,
			ProvAppAgents: agents,
			Deployment:    config.NewDeploymentConfig(),
		},
	}
}

func TestAddAppAndAddAppToList_UseSameInitialization(t *testing.T) {
	left := newTestClusterForAddApp(t, "c1")
	right := newTestClusterForAddApp(t, "c1")

	appFromAdd := newTestAppForAddApp("app1", "8080", "a1,a2")
	left.AddApp(appFromAdd)

	appFromList := newTestAppForAddApp("app1", "8080", "a1,a2")
	list := []*App{}
	right.addAppToList(&list, appFromList)

	if len(list) != 1 {
		t.Fatalf("expected addAppToList to append one app, got %d", len(list))
	}

	if appFromAdd.ServiceName != appFromList.ServiceName {
		t.Fatalf("service name mismatch: AddApp=%q addAppToList=%q", appFromAdd.ServiceName, appFromList.ServiceName)
	}
	if appFromAdd.State != appFromList.State {
		t.Fatalf("state mismatch: AddApp=%q addAppToList=%q", appFromAdd.State, appFromList.State)
	}
	if appFromAdd.Id != appFromList.Id {
		t.Fatalf("id mismatch: AddApp=%q addAppToList=%q", appFromAdd.Id, appFromList.Id)
	}

	wantDatadir := filepath.Join(left.Conf.WorkingDir, "c1", "apps", "app1")
	if appFromAdd.Datadir != wantDatadir {
		t.Fatalf("unexpected datadir: got %q want %q", appFromAdd.Datadir, wantDatadir)
	}
	wantDatadir = filepath.Join(right.Conf.WorkingDir, "c1", "apps", "app1")
	if appFromList.Datadir != wantDatadir {
		t.Fatalf("unexpected addAppToList datadir: got %q want %q", appFromList.Datadir, wantDatadir)
	}

	if appFromAdd.AppConfig.ProvAppCreditPlanned != 2 || appFromList.AppConfig.ProvAppCreditPlanned != 2 {
		t.Fatalf("expected planned credit inferred from agents to be 2, got AddApp=%d addAppToList=%d",
			appFromAdd.AppConfig.ProvAppCreditPlanned,
			appFromList.AppConfig.ProvAppCreditPlanned,
		)
	}

	if len(appFromAdd.AppConfig.Deployment.Routes) != 1 || len(appFromList.AppConfig.Deployment.Routes) != 1 {
		t.Fatalf("expected one default route on both paths")
	}
	if appFromAdd.AppConfig.Deployment.Routes[0].CName != appFromList.AppConfig.Deployment.Routes[0].CName {
		t.Fatalf("default route cname mismatch: AddApp=%q addAppToList=%q",
			appFromAdd.AppConfig.Deployment.Routes[0].CName,
			appFromList.AppConfig.Deployment.Routes[0].CName,
		)
	}
}
