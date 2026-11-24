// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"regexp"
	"sort"
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

// appCoresRe: a number of cores as docker --cpus takes it, whole or decimal (0.5, 1.5, 2).
// Strictly digits and one dot: it is a client setting written into run_args.
var appCoresRe = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,3})?$`)

// validateAppCores returns the docker --cpus value of a number of cores (a whole number is
// written N.0, as it always was), or an error naming the value.
func validateAppCores(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !appCoresRe.MatchString(raw) {
		return "", fmt.Errorf("prov-app-cpu-cores %q: a number of cores such as 2 or 1.5 is expected", raw)
	}
	cores, err := strconv.ParseFloat(raw, 64)
	if err != nil || cores <= 0 {
		return "", fmt.Errorf("prov-app-cpu-cores %q: a number of cores above 0 is expected", raw)
	}
	if !strings.Contains(raw, ".") {
		return raw + ".0", nil
	}
	return raw, nil
}

// appRunArgsLimits is the docker cap of a plain app (prov-db-docker-run-args-limit): its
// memory and cores written into run_args. Both are client settings: anything but a number
// is an error, which stops the rendering (never a container started uncapped).
func (cluster *Cluster) appRunArgsLimits(app *App) (string, error) {
	mem := cluster.GetAppMemory(app.AppConfig)
	memMB, err := config.ParseUnitMeasurementToInt("M,bytes,required", mem, true)
	if err != nil || memMB < 1 {
		return "", fmt.Errorf("prov-app-memory %q: a memory size such as 1024 or 2G is expected", mem)
	}
	cpus, err := validateAppCores(cluster.GetAppCores(app.AppConfig))
	if err != nil {
		return "", err
	}
	m := strconv.Itoa(memMB) + "m"
	return "--memory=" + m + " --memory-swap=" + m + " --cpus=" + cpus, nil
}

var (
	appVolumeOwnerRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	appVolumePermRe  = regexp.MustCompile(`^[0-7]{3,4}$`)
	appNumericIDRe   = regexp.MustCompile(`^[0-9]{1,10}$`)
	// a git clone or S3 mount name: it names a service section (container#NNinit<name>) and
	// the prefix of its environment keys (GIT_<NAME>_BRANCH...), referenced by the clone
	// command
	appStorageNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	// a git branch or tag: the git ref name rules, on a strict character set
	gitBranchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]{0,254}$`)
	// a repository host and path, the clone adds https://: no space, no quote, no '@' (the
	// credentials are added by the clone), no leading '-'
	gitRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/:%+-]{0,511}$`)
	// S3 values the mount sidecar's entrypoint word-splits into the mount command line
	s3EndpointRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/:%+-]{0,511}$`)
	s3BucketRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
	s3RegionRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidateGitBranch validates the branch (or tag) of a git clone: empty (the clone takes
// master) or a git ref name on a strict character set, never read as an option.
func ValidateGitBranch(name, branch string) error {
	if branch == "" {
		return nil
	}
	bad := !gitBranchRe.MatchString(branch) || strings.Contains(branch, "..") || strings.Contains(branch, "//") ||
		strings.Contains(branch, "/.") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") ||
		strings.HasSuffix(branch, ".lock")
	if bad {
		return fmt.Errorf("git clone %s branch %q: a git branch or tag name is expected (letters, digits, '.', '_', '/', '+', '-')", name, branch)
	}
	return nil
}

// ValidateGitRepository validates the repository of a git clone: a host and path, with or
// without https:// (http:// is cloned over https too). The clone runs over https: another
// scheme (ssh://, git@host:path, file://, ext::) is refused, it would not clone anyway.
func ValidateGitRepository(name, repo string) error {
	r := strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "http://")
	if !gitRepoRe.MatchString(r) || strings.Contains(r, "://") || strings.Contains(r, "..") {
		return fmt.Errorf("git clone %s repository %q: an https repository such as github.com/team/repo.git is expected (ssh and other schemes are not cloned)", name, repo)
	}
	return nil
}

// validateAppDeploymentForService refuses, before anything is rendered, a client value that
// would reach the service definition as something else than what it is for: an image that
// reads as a run option, a cap that is not a number, a volume whose name or directory is a
// path of the node, a git clone or S3 mount name, branch, repository or path that is not
// one, an owner or permission that is not one, a mount sidecar value that is not one word.
func (cluster *Cluster) validateAppDeploymentForService(app *App) error {
	appcnf := app.AppConfig
	if err := ValidateDockerImage("prov-app-docker-img", strings.TrimSpace(appcnf.ProvAppDockerImg)); err != nil {
		return err
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit && cluster.engineServerOfApp(app) == nil {
		if _, err := cluster.appRunArgsLimits(app); err != nil {
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
		if !appStorageNameRe.MatchString(gc.Name) {
			return fmt.Errorf("git clone name %q: letters, digits, '.', '_' and '-' only, 63 characters at most", gc.Name)
		}
		if err := ValidateGitBranch(gc.Name, gc.GitBranch); err != nil {
			return err
		}
		if err := ValidateGitRepository(gc.Name, gc.GitRepo); err != nil {
			return err
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
	for _, s3m := range deployment.Storages.S3Mounts {
		if s3m == nil {
			continue
		}
		if !appStorageNameRe.MatchString(s3m.Name) {
			return fmt.Errorf("S3 mount name %q: letters, digits, '.', '_' and '-' only, 63 characters at most", s3m.Name)
		}
		// arguments and environment of the privileged mount sidecar, whose entrypoint
		// word-splits them into the mount command: one word each, never an option
		if !appNumericIDRe.MatchString(s3m.MountUid()) || !appNumericIDRe.MatchString(s3m.MountGid()) {
			return fmt.Errorf("S3 mount %s uid/gid %q/%q: numeric ids are expected", s3m.Name, s3m.MountUid(), s3m.MountGid())
		}
		if s3m.Endpoint != "" && !s3EndpointRe.MatchString(s3m.Endpoint) {
			return fmt.Errorf("S3 mount %s endpoint %q: a host[:port] or URL is expected", s3m.Name, s3m.Endpoint)
		}
		if s3m.Bucket != "" && !s3BucketRe.MatchString(s3m.Bucket) {
			return fmt.Errorf("S3 mount %s bucket %q: a bucket name is expected", s3m.Name, s3m.Bucket)
		}
		if s3m.Region != "" && !s3RegionRe.MatchString(s3m.Region) {
			return fmt.Errorf("S3 mount %s region %q: a region name is expected", s3m.Name, s3m.Region)
		}
		if s3m.MountDir != "" {
			if err := config.ValidateMountPath("S3 mount "+s3m.Name+" mount directory", s3m.MountDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateAppServiceSections refuses a rendered service definition with a line break in a
// key or a value: written into the orchestrator's INI configuration, it would start a new
// keyword of the client's choosing.
func validateAppServiceSections(svcsection map[string]map[string]string) error {
	// sorted: the same definition always reports the same first error
	names := make([]string, 0, len(svcsection))
	for name := range svcsection {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("service section %q: line break refused", name)
		}
		section := svcsection[name]
		keys := make([]string, 0, len(section))
		for k := range section {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(section[k], "\r\n") {
				return fmt.Errorf("service keyword %s.%s: line break refused", name, k)
			}
		}
	}
	return nil
}
