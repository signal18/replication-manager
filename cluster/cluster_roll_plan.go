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
	switch strings.ToLower(strings.TrimSpace(target)) {
	case releases.TargetPatch, releases.TargetLastMinor:
		// The default upgrade follows the DECLARED image, not the line the nodes run:
		// a line resolves to its newest release in the list, latest / lts to what they
		// mean in the list, an explicit release to itself, an unknown tag to itself.
		// So declaring a higher line and running the default upgrade moves there.
		release = cat.Resolve(repo, flavor, tag)
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
	if running != "" && releases.CompareReleases(release, running) < 0 {
		switch strings.ToLower(strings.TrimSpace(target)) {
		case releases.TargetPatch, releases.TargetLastMinor:
			// The list knows nothing newer than what runs: the running release is the
			// answer, the upgrade re-pulls it.
			warnings = append(warnings, "the "+cat.Source+" has nothing newer than the running "+running+" on line "+current.String()+": the service definitions are pinned on it")
			release = running
			next = current
		default:
			return nil, fmt.Errorf("downgrade from %s to %s refused: a rolling upgrade only moves forward", running, release)
		}
	}
	if next.Less(current) {
		return nil, fmt.Errorf("downgrade from %s to %s refused: a rolling upgrade only moves forward (a replica older than its master cannot replicate and the data dictionary does not go back); restore a backup taken on %s instead", current, release, release)
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
	if next.Major > current.Major {
		warnings = append(warnings, "major upgrade: the engine runs mariadb-upgrade (MARIADB_AUTO_UPGRADE) on first start, check it in the error log of each node; there is no rolling way back to "+current.String())
	} else if current.Less(next) {
		warnings = append(warnings, "no rolling way back to "+current.String()+" once a replica runs "+next.String()+": replication from a newer master to an older replica is not supported")
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
		steps = append(steps, "rolling upgrade: pull the image and restart each replica, switchover, pull and restart the old master")
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
				return plan, fmt.Errorf("service definition push failed on %s: %w", srv.URL, err)
			}
		}
	}
	return plan, nil
}
