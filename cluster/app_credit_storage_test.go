package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newAppForCreditStorageTests(plannedCredits int, diskUnit string, volumes config.AppVolumes) *App {
	cl := &Cluster{
		Conf: &config.Config{
			ProvAppDisk:     diskUnit,
			ProvAppMem:      "4096",
			ProvAppCpuCores: "1",
		},
	}
	app := &App{
		ClusterGroup: cl,
		AppConfig: &config.AppConfig{
			ProvAppCreditPlanned: plannedCredits,
			ProvAppAgents:        "agent1",
			Deployment: &config.Deployment{
				AppVolumes: volumes,
			},
		},
	}
	return app
}

func TestValidateCanonicalVolumeSizeForCredits(t *testing.T) {
	app := newAppForCreditStorageTests(3, "10", nil)

	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "10g"); err != nil {
		t.Fatalf("expected 10g to be accepted, got %v", err)
	}
	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "20g"); err != nil {
		t.Fatalf("expected 20g to be accepted, got %v", err)
	}
	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "5g"); err == nil {
		t.Fatal("expected 5g to be rejected")
	}
	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "15g"); err == nil {
		t.Fatal("expected 15g to be rejected")
	}
}

func TestValidateCanonicalVolumeSizeForCredits_AllowsGrandfatheredUnchangedSize(t *testing.T) {
	volumes := config.AppVolumes{{Name: "legacy", Pool: "tank", Size: "4g"}}
	app := newAppForCreditStorageTests(1, "10", volumes)

	if err := app.ValidateCanonicalVolumeSizeForCredits(volumes, "legacy", "4g"); err != nil {
		t.Fatalf("expected unchanged legacy size to be accepted, got %v", err)
	}
	if err := app.ValidateCanonicalVolumeSizeForCredits(volumes, "legacy", "8g"); err == nil {
		t.Fatal("expected changed undersized legacy volume to be rejected")
	}
}

func TestValidateCanonicalVolumeSizeForCredits_AllowsAdvancedSizeWhenEnabled(t *testing.T) {
	app := newAppForCreditStorageTests(3, "10", nil)
	app.ClusterGroup.Conf.ProvAppVolumeAllowAdvancedSize = true

	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "15g"); err != nil {
		t.Fatalf("expected advanced size to be accepted when setting is enabled, got %v", err)
	}
	if err := app.ValidateCanonicalVolumeSizeForCredits(nil, "", "5g"); err == nil {
		t.Fatal("expected advanced size below the minimum credit unit to be rejected")
	}
}

func TestValidateCanonicalVolumeBudget(t *testing.T) {
	volumes := config.AppVolumes{
		{Name: "vol-a", Pool: "tank", Size: "10g"},
		{Name: "vol-b", Pool: "tank", Size: "10g"},
	}
	app := newAppForCreditStorageTests(3, "10", volumes)

	if err := app.ValidateCanonicalVolumeBudget(volumes, "", "10g"); err != nil {
		t.Fatalf("expected adding third 10g volume to fit budget, got %v", err)
	}
	if err := app.ValidateCanonicalVolumeBudget(volumes, "", "20g"); err == nil {
		t.Fatal("expected adding 20g volume to exceed budget")
	}
	if err := app.ValidateCanonicalVolumeBudget(volumes, "vol-b", "20g"); err != nil {
		t.Fatalf("expected replacing 10g with 20g to fit budget, got %v", err)
	}
}

func TestSetAppProvisionByCredit_BlocksWhenAllocatedStorageExceedsNewBudget(t *testing.T) {
	volumes := config.AppVolumes{
		{Name: "vol-a", Pool: "tank", Size: "10g"},
		{Name: "vol-b", Pool: "tank", Size: "20g"},
	}
	app := newAppForCreditStorageTests(3, "10", volumes)
	app.AppConfig.ProvAppCreditUsed = 3

	if err := app.SetAppProvisionByCredit(2); err == nil {
		t.Fatal("expected reducing credits below allocated canonical storage to fail")
	}
}

func TestSetAppProvisionByCredit_BlocksInvalidExistingVolumeWhenEnablingCredits(t *testing.T) {
	volumes := config.AppVolumes{
		{Name: "legacy", Pool: "tank", Size: "4g"},
	}
	app := newAppForCreditStorageTests(0, "10", volumes)

	if err := app.SetAppProvisionByCredit(1); err == nil {
		t.Fatal("expected enabling credits with invalid existing canonical volume size to fail")
	}
}

func TestSetAppProvisionByCredit_AllowsAdvancedSizedExistingVolumeWhenEnabled(t *testing.T) {
	volumes := config.AppVolumes{
		{Name: "advanced", Pool: "tank", Size: "15g"},
	}
	app := newAppForCreditStorageTests(0, "10", volumes)
	app.ClusterGroup.Conf.ProvAppVolumeAllowAdvancedSize = true

	if err := app.SetAppProvisionByCredit(2); err != nil {
		t.Fatalf("expected enabling credits with advanced-sized existing volume to succeed, got %v", err)
	}
}

func TestSetAppProvisionByCredit_UpdatesResourcesWithNewDefaults(t *testing.T) {
	app := newAppForCreditStorageTests(1, "10", nil)
	app.AppConfig.ProvAppCreditPlanned = 0

	if err := app.SetAppProvisionByCredit(1); err != nil {
		t.Fatalf("unexpected error setting credits: %v", err)
	}
	if got, want := app.AppConfig.ProvAppCpuCores, "1"; got != want {
		t.Fatalf("expected cpu cores %q, got %q", want, got)
	}
	if got, want := app.AppConfig.ProvAppMem, "4096"; got != want {
		t.Fatalf("expected memory %q, got %q", want, got)
	}
	if got, want := app.AppConfig.ProvAppDisk, "10"; got != want {
		t.Fatalf("expected disk %q, got %q", want, got)
	}
}

func TestValidateCanonicalDeploymentForCredits_GrandfathersExistingVolumeSizes(t *testing.T) {
	currentVolumes := config.AppVolumes{{Name: "legacy", Pool: "tank", Size: "4g"}}
	app := newAppForCreditStorageTests(1, "10", currentVolumes)

	candidate := &config.Deployment{AppVolumes: config.AppVolumes{{Name: "legacy", Pool: "tank", Size: "4g"}}}
	if err := app.ValidateCanonicalDeploymentForCredits(candidate); err != nil {
		t.Fatalf("expected unchanged legacy deployment to pass, got %v", err)
	}

	badCandidate := &config.Deployment{AppVolumes: config.AppVolumes{{Name: "legacy", Pool: "tank", Size: "15g"}}}
	if err := app.ValidateCanonicalDeploymentForCredits(badCandidate); err == nil {
		t.Fatal("expected invalid resized deployment to fail")
	}
}
