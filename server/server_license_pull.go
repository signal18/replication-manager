// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// license-pull (#2309): a replication-manager without internet access loads, from a package
// file in its datadir, what a git pull of its -pull repo would bring. The package's manifest
// says where each file goes and which log plugin wire and platform it was built for;
// license-pull refuses another one, places the files where a pull puts them, then runs the
// steps a pull runs (applyPullDir), cloud18.toml included even when cloud18 is off.

const licensePullStateKey = "GWARN023@license-pull"

// LicensePullResult answers a license-pull: the plugins of the package loaded and refused.
type LicensePullResult struct {
	Package   string   `json:"package"`
	Checksum  string   `json:"checksum"`
	Unchanged bool     `json:"unchanged"`
	Loaded    []string `json:"loaded"`
	Refused   []string `json:"refused"`
}

// licensePullBaseByKind maps each manifest kind to the directory a pull fills: the plugin
// binaries and the instance-wide files (cloud18.toml, a cluster's inject.toml, the public key)
// to the pull dir, the signatures to ShareDir/plugins, where the verify looks.
func (repman *ReplicationManager) licensePullBaseByKind(pullDir string) map[string]string {
	return map[string]string{
		"plugin":     filepath.Join(pullDir, logplugin.PluginDirName),
		"plugin-sig": filepath.Join(repman.Conf.ShareDir, "plugins"),
		"config":     pullDir,
		"pubkey":     pullDir,
	}
}

// resolveLicensePackage returns the absolute path of a package given by the user: relative
// paths are read under the datadir, and the file must be inside the datadir (no symlink or
// .. out of it).
func (repman *ReplicationManager) resolveLicensePackage(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no package path")
	}
	dataDir, err := filepath.EvalSymlinks(repman.Conf.WorkingDir)
	if err != nil {
		return "", fmt.Errorf("datadir %s: %w", repman.Conf.WorkingDir, err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repman.Conf.WorkingDir, path)
	}
	full, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("package %s: %w", path, err)
	}
	rel, err := filepath.Rel(dataDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("package %s is not in the datadir %s", path, repman.Conf.WorkingDir)
	}
	return full, nil
}

// LicensePull loads the package at path as a pull would. The checksum of the last package
// loaded is kept: the same package loaded again changes nothing, as an unchanged cloud18.toml
// at a pull. A refused package, or a plugin of it refused by the signature check, is kept as
// GWARN023 until the next license-pull. One license-pull at a time.
func (repman *ReplicationManager) LicensePull(path string) (LicensePullResult, error) {
	repman.licensePullMu.Lock()
	defer repman.licensePullMu.Unlock()
	res, err := repman.licensePull(path)
	switch {
	case err != nil:
		repman.licensePullRefused = err.Error()
	case len(res.Refused) > 0:
		repman.licensePullRefused = fmt.Sprintf("%s: plugin(s) %s refused by the signature check", res.Package, strings.Join(res.Refused, ", "))
	default:
		repman.licensePullRefused = ""
		repman.licensePullChecksum = res.Checksum
	}
	return res, err
}

// licensePull runs under licensePullMu.
func (repman *ReplicationManager) licensePull(path string) (LicensePullResult, error) {
	var res LicensePullResult
	// a repman that pulls its -pull repo gets the package through it: two writers of the
	// pull dir would race
	if repman.Conf.GitUrlPull != "" {
		return res, fmt.Errorf("this replication-manager pulls %s: the package comes through it", repman.Conf.GitUrlPull)
	}
	pkg, err := repman.resolveLicensePackage(path)
	if err != nil {
		return res, err
	}
	res.Package = pkg
	sum, err := repman.ComputeFileChecksum(pkg)
	if err != nil {
		return res, fmt.Errorf("package %s: %w", pkg, err)
	}
	res.Checksum = hex.EncodeToString(sum.Sum(nil))
	if res.Checksum == repman.licensePullChecksum && repman.licensePullRefused == "" {
		res.Unchanged = true
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
			"license-pull: package %s unchanged (checksum %s), nothing to load", pkg, res.Checksum)
		return res, nil
	}
	// a package built for another protocol or platform would load plugins that cannot run
	head, err := logplugin.ReadPackageManifest(pkg)
	if err != nil {
		return res, fmt.Errorf("package %s: %w", pkg, err)
	}
	if head.Wire != wire.WireVersion {
		return res, fmt.Errorf("package %s built for wire %d, this replication-manager is wire %d", pkg, head.Wire, wire.WireVersion)
	}
	if platform := runtime.GOOS + "/" + runtime.GOARCH; head.Platform != platform {
		return res, fmt.Errorf("package %s built for platform %q, this replication-manager is %s", pkg, head.Platform, platform)
	}
	pullDir := filepath.Join(repman.Conf.WorkingDir, ".pull")
	man, err := logplugin.ExtractPackage(pkg, repman.licensePullBaseByKind(pullDir))
	if err != nil {
		return res, fmt.Errorf("package %s: %w", pkg, err)
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
		"license-pull: package %s extracted (%d files), applying it as a pull", pkg, len(man.Entries))
	repman.applyPullDir(pullDir, true)

	// a plugin is loaded when the pull copied it to the shared dir: the copy follows the
	// signature check
	sharedDir := filepath.Join(repman.Conf.WorkingDir, logplugin.PluginDirName)
	for _, e := range man.Entries {
		if e.Kind != "plugin" {
			continue
		}
		name := filepath.Base(e.Dest)
		if gitPluginFilesEqual(filepath.Join(pullDir, logplugin.PluginDirName, name), filepath.Join(sharedDir, name)) {
			res.Loaded = append(res.Loaded, name)
		} else {
			res.Refused = append(res.Refused, name)
		}
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
		"license-pull: package %s, plugins loaded %v, refused %v", pkg, res.Loaded, res.Refused)
	return res, nil
}

// ProduceLicensePullState keeps GWARN023 open while the last license-pull was refused.
func (repman *ReplicationManager) ProduceLicensePullState() {
	// TryLock: a license-pull in progress holds the lock; the tick never waits for it (F2)
	if !repman.licensePullMu.TryLock() {
		repman.PreserveState(licensePullStateKey)
		return
	}
	refused := repman.licensePullRefused
	repman.licensePullMu.Unlock()
	if refused == "" {
		return
	}
	repman.SetState(licensePullStateKey, state.State{ErrType: "WARNING", ErrKey: "GWARN023",
		ErrDesc: fmt.Sprintf(config.GlobalError["GWARN023"], refused), ErrFrom: "REPMAN"})
}

type licensePullRequest struct {
	Path string `json:"path"`
}

// handlerMuxLicensePull godoc
// @Summary Load a -pull package from a file (license-pull)
// @Description For a replication-manager without internet access: the package (tar.gz with a manifest.json) is read from the datadir, its files are placed where a git pull of the -pull repo puts them, then the steps of a pull run (cloud18.toml applied even when cloud18 is off, plugins verified with plugin-signing-public-key and loaded). Refused on a replication-manager that pulls its -pull repo (git-url-pull set). A refused package opens GWARN023.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param body body licensePullRequest true "path of the package, absolute or relative to the datadir"
// @Success 200 {object} LicensePullResult
// @Failure 400 {string} string "Package refused"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/settings/actions/license-pull [post]
func (repman *ReplicationManager) handlerMuxLicensePull(w http.ResponseWriter, r *http.Request) {
	if _, ok := repman.cloud18IdentityACL(w, r); !ok {
		return
	}
	var req licensePullRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "Decode body: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := repman.LicensePull(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
