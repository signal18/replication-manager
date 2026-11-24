// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// Sandbox runtime of the client app containers (prov-app-docker-runtime). The client
// chooses the image and the command of an app freely; the isolation from the node comes
// from the runtime of every container that runs that image, and from what the client's
// values can NOT reach in the service definition: the run options, the mounts, the
// privileged sidecars. See doc/implementation/cluster/APP_SANDBOX_RUNTIME.md.

// appImageReference is the image keyword of every section that runs the client's image
// (the app, its jobs sidecar): the runtime goes on exactly those. Under the sandbox the
// reload task runs in container#app itself (openSVCAddAppReloadSection).
const appImageReference = "{env.app_img}"

// applyAppDockerRuntime adds --runtime=<runtime> at the END of the run_args of every
// docker/podman section that runs the client's image, when the instance sets one. Last on
// the line, it is the one docker keeps whatever precedes it. The pause container, the
// sensor and the init or mount sidecars run images of ours and keep the node's runtime.
func (cluster *Cluster) applyAppDockerRuntime(svcsection map[string]map[string]string) error {
	runtime, err := cluster.Conf.GetAppDockerRuntime()
	if err != nil {
		return err
	}
	if runtime == "" {
		return nil
	}
	for _, section := range svcsection {
		if section["image"] != appImageReference {
			continue
		}
		if t := section["type"]; t != "docker" && t != "podman" {
			continue
		}
		section["run_args"] = strings.TrimSpace(section["run_args"] + " --runtime=" + runtime)
	}
	return nil
}

// appCoresForRunArgs is the whole number of cores of the app's docker --cpus: the value is
// a client setting written into run_args, anything but a number would be a run option.
func (cluster *Cluster) appCoresForRunArgs(app *App) (int, error) {
	raw := strings.TrimSpace(cluster.GetAppCores(app.AppConfig))
	cores, err := strconv.Atoi(raw)
	if err != nil || cores < 1 {
		return 0, fmt.Errorf("prov-app-cpu-cores %q: a whole number of cores >= 1 is expected", raw)
	}
	return cores, nil
}

var (
	appVolumeOwnerRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	appVolumePermRe  = regexp.MustCompile(`^[0-7]{3,4}$`)
	appNumericIDRe   = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// validateAppDeploymentForService refuses, before anything is rendered, a client value that
// would reach the service definition as something else than what it is for: an image that
// reads as a run option, a volume whose name or directory is a path of the node, a git
// clone path that leaves its quotes, an owner or permission that is not one, a mount
// sidecar option that is not a number, a loader variable in the environment of the
// privileged S3 mount sidecar.
func (cluster *Cluster) validateAppDeploymentForService(app *App) error {
	appcnf := app.AppConfig
	if err := ValidateDockerImage("prov-app-docker-img", strings.TrimSpace(appcnf.ProvAppDockerImg)); err != nil {
		return err
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit && cluster.engineServerOfApp(app) == nil {
		if _, err := cluster.appCoresForRunArgs(app); err != nil {
			return err
		}
	}
	deployment := appcnf.Deployment
	if deployment == nil {
		return nil
	}
	for _, vol := range deployment.Storages.Volumes {
		if vol == nil {
			continue
		}
		if err := config.ValidateVolumeName(vol.Name); err != nil {
			return err
		}
		for _, dir := range vol.GetVolumeDirs() {
			if err := config.ValidateMountPath("volume "+vol.Name+" directory", dir); err != nil {
				return err
			}
		}
		if v := strings.TrimSpace(vol.User); v != "" && !appVolumeOwnerRe.MatchString(v) {
			return fmt.Errorf("volume %s user %q: a user name or id is expected", vol.Name, v)
		}
		if v := strings.TrimSpace(vol.Group); v != "" && !appVolumeOwnerRe.MatchString(v) {
			return fmt.Errorf("volume %s group %q: a group name or id is expected", vol.Name, v)
		}
		if v := strings.TrimSpace(vol.DirPerm); v != "" && !appVolumePermRe.MatchString(v) {
			return fmt.Errorf("volume %s dirperm %q: an octal mode such as 0755 is expected", vol.Name, v)
		}
	}
	for _, p := range deployment.Paths {
		// the directories the volume sections create come from these paths too
		for _, dir := range []string{p.SourcePath, p.DockerPath} {
			if dir == "" || dir == "." {
				continue
			}
			if err := config.ValidateMountPath("path mapping "+p.DockerPath, dir); err != nil {
				return err
			}
		}
	}
	for _, gc := range deployment.Storages.GitClones {
		if gc == nil {
			continue
		}
		if _, err := config.VolumeSource(gc.GetSourceVolumeName(), "."); err != nil {
			return fmt.Errorf("git clone %s: %w", gc.Name, err)
		}
		if gc.VolumeDir != "" {
			if err := config.ValidateMountPath("git clone "+gc.Name+" directory", gc.VolumeDir); err != nil {
				return err
			}
		}
	}
	if len(deployment.Storages.S3Mounts) > 0 {
		for _, s3m := range deployment.Storages.S3Mounts {
			if s3m == nil {
				continue
			}
			if !appNumericIDRe.MatchString(s3m.MountUid()) || !appNumericIDRe.MatchString(s3m.MountGid()) {
				return fmt.Errorf("S3 mount %s uid/gid %q/%q: numeric ids are expected", s3m.Name, s3m.MountUid(), s3m.MountGid())
			}
		}
		// the S3 mount sidecar is privileged and reads the app's environment: a dynamic
		// loader variable there would load a library of the client's into it
		for _, v := range deployment.Variables {
			if isLoaderVariable(v.Name) {
				return fmt.Errorf("variable %s: dynamic loader variables (LD_*) are refused in an app with an S3 mount, whose mount sidecar shares the app's environment", v.Name)
			}
		}
	}
	return nil
}

// isLoaderVariable: an environment variable the dynamic loader reads (LD_PRELOAD,
// LD_LIBRARY_PATH, LD_AUDIT...).
func isLoaderVariable(name string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(name)), "LD_")
}

// validateAppServiceSections refuses a rendered service definition with a line break in a
// key or a value: written into the orchestrator's INI configuration, it would start a new
// keyword of the client's choosing.
func validateAppServiceSections(svcsection map[string]map[string]string) error {
	for name, section := range svcsection {
		if strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("service section %q: line break refused", name)
		}
		for k, v := range section {
			if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("service keyword %s.%s: line break refused", name, k)
			}
		}
	}
	return nil
}
