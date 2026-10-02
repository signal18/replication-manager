// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// loadImageCatalog builds the image list catalog of a cluster, a variable for tests.
var loadImageCatalog = func(cluster *Cluster) (*releases.Catalog, error) {
	table, _, err := releases.Load(cluster.Conf.ShareDir + "/plugins/data")
	if err != nil {
		return nil, err
	}
	repos, err := cluster.Conf.GetDockerRepos(cluster.Conf.ShareDir+"/repo/repos.json", cluster.Conf.Test)
	if err != nil {
		return nil, err
	}
	tags := map[string][]releases.Tag{}
	for _, r := range repos {
		for _, t := range r.Tags.Results {
			tags[r.Image] = append(tags[r.Image], releases.Tag{Name: t.Name, Digest: t.Digest})
		}
	}
	source := "embedded image list"
	if st, err := os.Stat(filepath.Join(cluster.Conf.ShareDir, "plugins", "data", "repos.json")); err == nil && st.Size() > 0 {
		source = "plugins/data/repos.json (back office)"
	}
	return &releases.Catalog{Table: table, Tags: tags, Source: source}, nil
}

// ImageCatalog is the configurator's image list for this instance.
func (cluster *Cluster) ImageCatalog() (*releases.Catalog, error) {
	return loadImageCatalog(cluster)
}

// RollingUpgradePlan describes what a rolling upgrade to a target would do, computed
// from the image list only (#1862).
type RollingUpgradePlan struct {
	Cluster        string                   `json:"cluster"`
	Orchestrator   string                   `json:"orchestrator"`
	Flavor         string                   `json:"flavor"`
	CurrentImage   string                   `json:"currentImage"`   // prov-db-image as declared
	CurrentRelease string                   `json:"currentRelease"` // the release the service definitions carry (prov-db-docker-img-resolved)
	CurrentLine    string                   `json:"currentLine"`
	CurrentIsLTS   bool                     `json:"currentIsLTS"`
	Nodes          []map[string]interface{} `json:"nodes"`
	Target         string                   `json:"target"`
	TargetLine     string                   `json:"targetLine"`
	TargetImage    string                   `json:"targetImage"` // the real release, repo:x.y.z
	TargetIsLTS    bool                     `json:"targetIsLTS"`
	DeclaredAfter  string                   `json:"declaredAfter"` // prov-db-image after the upgrade
	Mechanic       string                   `json:"mechanic"`      // "upgrade" (restart on the new image) or "reprov" (provision again + reseed)
	Order          []string                 `json:"order"`
	Steps          []string                 `json:"steps"`
	Warnings       []string                 `json:"warnings"`
	ImageList      string                   `json:"imageList"`
	Status         string                   `json:"status,omitempty"`
}

// PlanRollingUpgrade resolves the target with the image list: the default (patch)
// resolves the declared prov-db-image (a line to its newest release, latest / lts to
// what the list says); next-minor, next-lts, next-major, last-lts and version move
// the declaration from the line the nodes run. Nothing is touched.
func (cluster *Cluster) PlanRollingUpgrade(target, explicit string) (*RollingUpgradePlan, error) {
	cat, err := cluster.ImageCatalog()
	if err != nil {
		return nil, err
	}
	declared := strings.TrimSpace(cluster.Conf.ProvDbImg)
	repo, tag := releases.SplitImage(declared)
	flavor := releases.FlavorOfImage(declared)
	var current releases.Line
	currentKnown := false
	running := "" // the master's release, x.y.z, when known
	nodes := []map[string]interface{}{}
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}
		n := map[string]interface{}{"server": srv.URL, "state": srv.State, "version": ""}
		if v := srv.DBVersion; v != nil {
			n["version"] = v.ToString()
			if srv.IsMaster() || !currentKnown {
				current = releases.Line{Major: v.Major, Minor: v.Minor}
				running = fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Release)
				currentKnown = true
				if v.Flavor != "" {
					flavor = strings.ToLower(v.Flavor)
				}
			}
		}
		nodes = append(nodes, n)
	}
	if !currentKnown {
		if l, err := releases.LineOf(tag); err == nil && tag != "latest" && tag != "lts" {
			current = l
		} else if r := cluster.resolvedImageFor(declared); r != "" {
			_, rt := releases.SplitImage(r)
			if l, err := releases.LineOf(rt); err == nil {
				current = l
			} else {
				return nil, fmt.Errorf("current release line unknown: no node reports a version and %s carries no line", declared)
			}
		} else {
			return nil, fmt.Errorf("current release line unknown: no node reports a version and prov-db-image %q carries no line", declared)
		}
	}
	if strings.TrimSpace(target) == "" {
		target = releases.TargetPatch
	}
	var release string
	pointerGuess := "" // latest / lts resolved by the newest release of the list, no digest
	switch strings.ToLower(strings.TrimSpace(target)) {
	case releases.TargetPatch, releases.TargetLastMinor:
		// The default upgrade follows the DECLARED image, not the line the nodes run:
		// a line resolves to its newest release in the list, latest / lts to what they
		// mean in the list, an explicit release to itself, an unknown tag to itself.
		// So declaring a higher line and running the default upgrade moves there.
		release = cat.Resolve(repo, flavor, tag)
		if (tag == "latest" || tag == "lts") && release != tag && !cat.HasDigest(repo, tag) {
			pointerGuess = tag
		}
	default:
		var err error
		if release, err = cat.Target(repo, flavor, current, target, explicit); err != nil {
			return nil, err
		}
	}
	next, lerr := releases.LineOf(release)
	if lerr != nil {
		next = current
	}
	warnings := []string{}
	// A downgrade is never refused (Stéphane 2026-10-02), it is announced. One case is
	// not a downgrade but a stale list: the declared line is the running line and the
	// list knows nothing newer than what runs; the running release is then the answer.
	if running != "" && releases.CompareReleases(release, running) < 0 && next == current {
		warnings = append(warnings, "the "+cat.Source+" has nothing newer than the running "+running+" on line "+current.String()+": the service definitions stay on it")
		release = running
	}
	// The mechanic: a data directory rewritten by a newer major cannot start on the
	// older one, so a move down across a major provisions each node again from scratch
	// and reseeds it (rolling reprov); a move up across a major does the same when
	// prov-db-upgrade-major-reprov is on, else the node restarts on the new image and
	// the engine runs mariadb-upgrade; every other move restarts on the new image.
	mechanic := "upgrade"
	switch {
	case next.Major < current.Major:
		mechanic = "reprov"
		warnings = append(warnings, "downgrade across a major, from "+current.String()+" to "+next.String()+": each node is provisioned again from scratch on "+release+" and reseeded from the master with a logical dump; the switchover runs with switchover-lower-release on for the duration")
	case next.Major > current.Major && cluster.Conf.ProvDbUpgradeMajorReprov:
		mechanic = "reprov"
		warnings = append(warnings, "major upgrade with prov-db-upgrade-major-reprov: each node is provisioned again from scratch on "+release+" and reseeded from the master; there is no rolling way back to "+current.String())
	case next.Less(current):
		warnings = append(warnings, "downgrade from line "+current.String()+" to "+release+" on the same major: each node restarts on the older image with its data directory; the switchover runs with switchover-lower-release on for the duration")
	case next == current && running != "" && releases.CompareReleases(release, running) < 0:
		warnings = append(warnings, "downgrade from "+running+" to "+release+" on the same line: each node restarts on the older image with its data directory")
	}
	onprem := cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise
	targetImage := repo + ":" + release
	// What prov-db-image declares after the upgrade: a patch keeps the declaration
	// (a line, latest, lts stay what they are), a given release is declared as is, a
	// line move declares the new line.
	declaredAfter := declared
	switch strings.ToLower(strings.TrimSpace(target)) {
	case releases.TargetPatch, releases.TargetLastMinor:
	case releases.TargetVersion:
		if releases.IsExplicitTag(strings.TrimSpace(explicit)) {
			declaredAfter = targetImage
		} else {
			declaredAfter = repo + ":" + next.String()
		}
	default:
		declaredAfter = repo + ":" + next.String()
	}
	if mechanic == "reprov" {
		// The gate (WARN0222 / WARN0223 / WARN0224): a reseed from a backup newer than
		// the binary log retention, with the binary logs monitored. Downward the backup
		// must be logical: a physical backup of the newer major does not restore into
		// the older one.
		r := cluster.GetReseedReadiness()
		issues := r.IssueTexts()
		if next.Major < current.Major && !r.LogicalFresh && len(issues) == 0 {
			issues = append(issues, "a downgrade across a major needs a logical backup of the primary newer than the binary log retention (a physical backup of "+current.String()+" does not restore into "+next.String()+"), arm autorejoin-logical-backup and take one")
		}
		if len(issues) > 0 {
			return nil, fmt.Errorf("rolling reprov across a major release refused: %s", strings.Join(issues, "; "))
		}
	}
	if next.Major > current.Major && mechanic == "upgrade" {
		warnings = append(warnings, "major upgrade in place: each node restarts on the new release with its data directory and the engine runs mariadb-upgrade on that first start (MARIADB_AUTO_UPGRADE=1 in the container environment, MariaDB images), check it in the error log of each node; there is no rolling way back to "+current.String())
	} else if current.Less(next) && mechanic == "upgrade" {
		warnings = append(warnings, "no rolling way back to "+current.String()+" once a replica runs "+next.String()+": replication from a newer master to an older replica is not supported")
	}
	if pointerGuess != "" {
		warnings = append(warnings, "prov-db-image "+declared+" resolved to "+release+" as the newest release of the "+cat.Source+": the list carries no digest for "+pointerGuess+", so this is what the list knows, not what the registry serves under "+pointerGuess+" today")
	}
	if !cat.InList(repo, release) {
		warnings = append(warnings, repo+":"+release+" is not in the "+cat.Source+": the orchestrator pulls what the registry has under that name")
	}
	if !cat.Table.IsLTS(flavor, next) && next != current {
		warnings = append(warnings, next.String()+" is not a long-term line for "+flavor+" (lts: "+strings.Join(cat.Table.LTS[flavor], ", ")+")")
	}
	steps := []string{}
	switch {
	case onprem:
		steps = []string{"run onpremise-ssh-upgrade-db-script on each node"}
		warnings = append(warnings, "on-premise orchestrator: the image is not changed, the rolling upgrade runs onpremise-ssh-upgrade-db-script on each node")
	default:
		if declaredAfter != declared {
			steps = append(steps, "set prov-db-image to "+declaredAfter)
			if cluster.IsVariableImmutable("prov-db-docker-img") {
				warnings = append(warnings, "prov-db-image "+declared+" is pinned in the immutable configuration (cluster.d): the upgrade is refused until the operator changes the pin")
			}
		}
		steps = append(steps, "render the service definitions with "+targetImage+" (the release the image list gives for "+target+")")
		if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
			steps = append(steps, "push the service definition of every node (update-opensvc-template), inert until the node restarts")
		}
		if mechanic == "reprov" {
			steps = append(steps, "rolling reprov: unprovision, provision on the new image and reseed each replica from the master, switchover, the same for the old master")
		} else {
			steps = append(steps, "rolling upgrade: pull the image and restart each replica, switchover, pull and restart the old master")
		}
	}
	order := []string{}
	for _, sl := range cluster.GetSlaves() {
		if sl != nil {
			order = append(order, sl.URL)
		}
	}
	if m := cluster.GetMaster(); m != nil {
		order = append(order, "switchover", m.URL)
	}
	return &RollingUpgradePlan{
		Cluster:        cluster.Name,
		Orchestrator:   cluster.GetOrchestrator(),
		Flavor:         flavor,
		CurrentImage:   declared,
		CurrentRelease: cluster.Conf.ProvDbImgResolved,
		CurrentLine:    current.String(),
		CurrentIsLTS:   cat.Table.IsLTS(flavor, current),
		Nodes:          nodes,
		Target:         target,
		TargetLine:     next.String(),
		TargetImage:    targetImage,
		TargetIsLTS:    cat.Table.IsLTS(flavor, next),
		DeclaredAfter:  declaredAfter,
		Mechanic:       mechanic,
		Order:          order,
		Steps:          steps,
		Warnings:       warnings,
		ImageList:      cat.Source,
	}, nil
}

// PrepareRollingUpgrade does, synchronously, what the plan announces before the
// rolling part: declares the image, pins the service definitions on the target
// release and, on OpenSVC, pushes them node by node (inert until the node restarts).
// The caller then starts RollingUpgrade. Nothing is touched when a step is refused.
func (cluster *Cluster) PrepareRollingUpgrade(target, explicit string) (*RollingUpgradePlan, error) {
	plan, err := cluster.PlanRollingUpgrade(target, explicit)
	if err != nil {
		return nil, err
	}
	if cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return plan, nil
	}
	// The image list is the only source of releases: a release it does not know is
	// refused before anything is declared or pushed (a typo would only fail at the pull,
	// after the definitions carry it).
	if cat, err := cluster.ImageCatalog(); err == nil {
		if repo, release := releases.SplitImage(plan.TargetImage); !cat.InList(repo, release) {
			return plan, fmt.Errorf("release %s is not in the %s: refresh the image list or move to a line it knows", plan.TargetImage, plan.ImageList)
		}
	}
	// Declaration and record first (the definitions render from them), pushes next; a
	// failed push restores the declaration and the record so nothing stays half done.
	prevImg, prevRecord := cluster.Conf.ProvDbImg, cluster.Conf.ProvDbImgResolved
	if plan.DeclaredAfter != plan.CurrentImage {
		if err := cluster.SetProvDBImage(plan.DeclaredAfter); err != nil {
			return plan, err
		}
	}
	record := plan.DeclaredAfter + "=" + plan.TargetImage
	if record != cluster.Conf.ProvDbImgResolved {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Rolling upgrade (%s): prov-db-image %s pinned on %s from the %s", target, plan.DeclaredAfter, plan.TargetImage, plan.ImageList)
		cluster.Conf.ProvDbImgResolved = record
		cluster.Save()
	}
	plan.CurrentRelease = record
	if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		for _, srv := range cluster.Servers {
			if srv == nil {
				continue
			}
			if err := cluster.OpenSVCUpdateDatabaseTemplate(srv); err != nil {
				cluster.Conf.ProvDbImg, cluster.Conf.ProvDbImgResolved = prevImg, prevRecord
				cluster.Save()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Rolling upgrade (%s): service definition push failed on %s, declaration restored to %s: %s", target, srv.URL, prevImg, err)
				return plan, fmt.Errorf("service definition push failed on %s, declaration restored: %w", srv.URL, err)
			}
		}
	}
	return plan, nil
}

// RunRollingUpgrade runs the rolling part the plan announced, after
// PrepareRollingUpgrade: the rolling reprov or the rolling upgrade. For a move down
// across lines it pilots switchover-lower-release on for the duration; for a move down
// across a major it pilots the reseed from the logical backup only (a physical backup
// of the newer major cannot restore into the older one); for any reprov it turns the
// direct dump off. The operator's values are restored afterwards.
func (cluster *Cluster) RunRollingUpgrade(plan *RollingUpgradePlan) error {
	if plan == nil {
		return cluster.RollingUpgrade()
	}
	current, _ := releases.ParseLine(plan.CurrentLine)
	next, _ := releases.ParseLine(plan.TargetLine)
	if next.Less(current) {
		saved := cluster.Conf.SwitchLowerRelease
		cluster.Conf.SwitchLowerRelease = true
		defer func() { cluster.Conf.SwitchLowerRelease = saved }()
	}
	if plan.Mechanic == "reprov" {
		// The reseed comes from a backup (the gate checked one exists), never from the
		// direct dump: the primary's own jobs container took the backup with a client of
		// its release, the repman host's dump client may be older than the primary.
		// Downward only the logical backup restores into the older major.
		savedDump, savedPhysical := cluster.Conf.AutorejoinMysqldump, cluster.Conf.AutorejoinPhysicalBackup
		cluster.Conf.AutorejoinMysqldump = false
		if next.Major < current.Major {
			cluster.Conf.AutorejoinPhysicalBackup = false
		}
		defer func() {
			cluster.Conf.AutorejoinMysqldump, cluster.Conf.AutorejoinPhysicalBackup = savedDump, savedPhysical
		}()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling upgrade (%s) to %s runs as a rolling reprov", plan.Target, plan.TargetImage)
		return cluster.RollingReprov()
	}
	return cluster.RollingUpgrade()
}
