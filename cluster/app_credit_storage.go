package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
)

const defaultAppCreditDiskUnitGiB = 10

func (app *App) AllowAdvancedCanonicalVolumeSize() bool {
	if app == nil || app.ClusterGroup == nil || app.ClusterGroup.Conf == nil {
		return false
	}
	return app.ClusterGroup.Conf.ProvAppVolumeAllowAdvancedSize
}

func (app *App) getAppCreditDiskUnitGiB() (int, error) {
	if app == nil || app.ClusterGroup == nil || app.ClusterGroup.Conf == nil {
		return defaultAppCreditDiskUnitGiB, nil
	}

	baseDisk := strings.TrimSpace(app.ClusterGroup.Conf.ProvAppDisk)
	if baseDisk == "" {
		baseDisk = fmt.Sprintf("%d", defaultAppCreditDiskUnitGiB)
	}

	return config.ParseUnitMeasurementToInt("G", baseDisk, true)
}

func sumCanonicalVolumeSizesGiB(volumes config.AppVolumes) (int, error) {
	total := 0
	for _, vol := range volumes {
		if vol == nil || strings.TrimSpace(vol.Size) == "" {
			continue
		}
		sizeGiB, err := parseVolumeSizeGiB(vol.Size)
		if err != nil {
			return 0, fmt.Errorf("invalid volume size %q for %q: %w", vol.Size, vol.Name, err)
		}
		total += sizeGiB
	}
	return total, nil
}

func parseVolumeSizeGiB(size string) (int, error) {
	return config.ParseUnitMeasurementToInt("G", size, true)
}

func unchangedGrandfatheredVolumeSizeGiB(currentVolumes config.AppVolumes, replacingName, newSize string) (bool, error) {
	if replacingName == "" {
		return false, nil
	}
	newSizeGiB, err := parseVolumeSizeGiB(newSize)
	if err != nil {
		return false, err
	}
	for _, vol := range currentVolumes {
		if vol == nil || vol.Name != replacingName || strings.TrimSpace(vol.Size) == "" {
			continue
		}
		existingSizeGiB, err := parseVolumeSizeGiB(vol.Size)
		if err != nil {
			return false, fmt.Errorf("invalid existing volume size %q for %q: %w", vol.Size, vol.Name, err)
		}
		return existingSizeGiB == newSizeGiB, nil
	}
	return false, nil
}

func validateCanonicalVolumeSizeAgainstUnit(currentVolumes config.AppVolumes, replacingName, size string, unitGiB int, allowGrandfather bool, enforceStep bool) error {
	sizeGiB, err := parseVolumeSizeGiB(size)
	if err != nil {
		return fmt.Errorf("invalid volume size %q: %w", size, err)
	}

	if allowGrandfather {
		unchangedGrandfathered, err := unchangedGrandfatheredVolumeSizeGiB(currentVolumes, replacingName, size)
		if err != nil {
			return err
		}
		if unchangedGrandfathered {
			return nil
		}
	}

	if sizeGiB < unitGiB {
		return fmt.Errorf("volume size must be at least %dg (1 credit)", unitGiB)
	}
	if enforceStep && sizeGiB%unitGiB != 0 {
		return fmt.Errorf("volume size must be a multiple of %dg (1 credit)", unitGiB)
	}

	return nil
}

func (app *App) GetCanonicalStorageCreditBudgetGiB(credits int) (int, int, error) {
	if credits <= 0 {
		return 0, 0, nil
	}

	unitGiB, err := app.getAppCreditDiskUnitGiB()
	if err != nil {
		return 0, 0, err
	}

	return credits * unitGiB, unitGiB, nil
}

func (app *App) ValidateCanonicalVolumeSizeForCredits(currentVolumes config.AppVolumes, replacingName, size string) error {
	if app == nil || app.AppConfig == nil || app.AppConfig.ProvAppCreditPlanned <= 0 {
		return nil
	}

	_, unitGiB, err := app.GetCanonicalStorageCreditBudgetGiB(app.AppConfig.ProvAppCreditPlanned)
	if err != nil {
		return err
	}
	enforceStep := !app.AllowAdvancedCanonicalVolumeSize()

	return validateCanonicalVolumeSizeAgainstUnit(currentVolumes, replacingName, size, unitGiB, true, enforceStep)
}

func (app *App) ValidateCanonicalVolumeBudget(currentVolumes config.AppVolumes, replacingName, newSize string) error {
	if app == nil || app.AppConfig == nil || app.AppConfig.ProvAppCreditPlanned <= 0 {
		return nil
	}

	budgetGiB, unitGiB, err := app.GetCanonicalStorageCreditBudgetGiB(app.AppConfig.ProvAppCreditPlanned)
	if err != nil {
		return err
	}

	existingGiB := 0
	for _, vol := range currentVolumes {
		if vol == nil || vol.Name == replacingName || strings.TrimSpace(vol.Size) == "" {
			continue
		}
		sizeGiB, err := parseVolumeSizeGiB(vol.Size)
		if err != nil {
			return fmt.Errorf("invalid existing volume size %q for %q: %w", vol.Size, vol.Name, err)
		}
		existingGiB += sizeGiB
	}

	newSizeGiB, err := parseVolumeSizeGiB(newSize)
	if err != nil {
		return fmt.Errorf("invalid volume size %q: %w", newSize, err)
	}

	totalGiB := existingGiB + newSizeGiB
	if totalGiB > budgetGiB {
		return fmt.Errorf("allocated canonical volume size %dg exceeds credit storage budget %dg (%d credits × %dg)", totalGiB, budgetGiB, app.AppConfig.ProvAppCreditPlanned, unitGiB)
	}

	return nil
}

func (app *App) ValidateCanonicalDeploymentForCredits(candidate *config.Deployment) error {
	if app == nil || app.AppConfig == nil {
		return nil
	}
	return app.validateCanonicalDeploymentForCredits(candidate, app.AppConfig.ProvAppCreditPlanned, true)
}

func (app *App) validateCanonicalDeploymentForCredits(candidate *config.Deployment, credits int, allowGrandfather bool) error {
	if app == nil || app.AppConfig == nil || credits <= 0 || candidate == nil {
		return nil
	}

	currentVolumes := config.AppVolumes(nil)
	if app.AppConfig.Deployment != nil {
		currentVolumes = app.AppConfig.Deployment.AppVolumes
	}

	budgetGiB, unitGiB, err := app.GetCanonicalStorageCreditBudgetGiB(credits)
	if err != nil {
		return err
	}
	if budgetGiB == 0 {
		return nil
	}
	enforceStep := !app.AllowAdvancedCanonicalVolumeSize()

	for _, vol := range candidate.AppVolumes {
		if vol == nil {
			continue
		}
		if err := validateCanonicalVolumeSizeAgainstUnit(currentVolumes, vol.Name, vol.Size, unitGiB, allowGrandfather, enforceStep); err != nil {
			return err
		}
	}

	allocatedGiB, err := sumCanonicalVolumeSizesGiB(candidate.AppVolumes)
	if err != nil {
		return err
	}
	if allocatedGiB > budgetGiB {
		return fmt.Errorf("allocated canonical volume size %dg exceeds credit storage budget %dg (%d credits × %dg)", allocatedGiB, budgetGiB, credits, unitGiB)
	}

	return nil
}

func (app *App) ValidateCanonicalStorageBudgetForCredits(credits int) error {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return nil
	}
	allowGrandfather := app.AppConfig.ProvAppCreditPlanned > 0
	if err := app.validateCanonicalDeploymentForCredits(app.AppConfig.Deployment, credits, allowGrandfather); err != nil {
		if allowGrandfather {
			return err
		}
		return fmt.Errorf("%w; adjust canonical volumes before changing credits", err)
	}
	return nil
}
