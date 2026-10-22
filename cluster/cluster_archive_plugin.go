// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// External archive plugins (#2091): a program archiveplugin-<binary> that keeps archives with
// its own engine embedded (archiveplugin-plakar: plakar's kloset). replication-manager loads
// their manifests once, then runs the program once per operation (cluster/archiveplugin/wire):
// the request on its stdin, the archive's secrets and S3 keys in its environment only.

const (
	archivePluginManifestTimeout = 15 * time.Second
	archivePluginOpTimeout       = 5 * time.Minute
	archivePluginLongOpTimeout   = 6 * time.Hour // push, restore, sync, purge, remove
)

var archivePluginsLoad sync.Once

// archivePluginDirs are where the plugin programs are: the delivered plugins
// (<WorkingDir>/plugins), else the ones built next to this binary (a source build).
func (cluster *Cluster) archivePluginDirs() []string {
	dirs := []string{filepath.Join(cluster.Conf.WorkingDir, logplugin.PluginDirName)}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "..", "plugins"))
	}
	return dirs
}

// archivePluginSigned checks the program's signature when the signing key is there, as for
// the log and replication plugins.
func (cluster *Cluster) archivePluginSigned(bin string) error {
	key := cluster.Conf.PluginSigningPublicKey
	if key == "" {
		return nil
	}
	if _, err := os.Stat(key); err != nil {
		return nil
	}
	if err := logplugin.VerifyPluginSignature(bin, filepath.Join(cluster.Conf.ShareDir, "plugins"), key); err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	return nil
}

// LoadArchivePlugins registers the manifests of the archive plugin programs found, once per
// process: a program that is not signed, or speaks another wire version, is left out.
func (cluster *Cluster) LoadArchivePlugins() {
	archivePluginsLoad.Do(func() {
		seen := map[string]bool{}
		for _, dir := range cluster.archivePluginDirs() {
			matches, _ := filepath.Glob(filepath.Join(dir, wire.BinaryPrefix+"*"))
			for _, bin := range matches {
				name := filepath.Base(bin)
				if seen[name] || strings.HasSuffix(name, ".sig") {
					continue
				}
				if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
					continue
				}
				seen[name] = true
				if err := cluster.loadArchivePlugin(bin); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Archive plugin %s not loaded: %s", name, err)
				}
			}
		}
	})
}

func (cluster *Cluster) loadArchivePlugin(bin string) error {
	if err := cluster.archivePluginSigned(bin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), archivePluginManifestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, wire.ManifestFlag)
	cmd.Env = archivePluginBaseEnv()
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%s: %w", wire.ManifestFlag, err)
	}
	list, err := wire.ReadManifests(bytes.NewReader(out))
	if err != nil {
		return err
	}
	for _, wm := range list {
		m := config.ArchivePluginManifest{Name: wm.Name, Description: wm.Description, Tier: wm.Tier, Capabilities: wm.Capabilities}
		for _, st := range wm.Settings {
			m.Settings = append(m.Settings, config.ArchivePluginSetting{Name: st.Name, Type: st.Type, Values: st.Values, Group: st.Group})
		}
		if err := config.RegisterArchivePlugin(m, bin); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Archive plugin %s loaded from %s (%s)", m.Name, filepath.Base(bin), m.Tier)
	}
	return nil
}

// archivePluginBaseEnv is the environment every plugin run gets: nothing of this process.
func archivePluginBaseEnv() []string {
	return []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + os.TempDir(),
		wire.EnvWireVersion + "=" + strconv.Itoa(wire.WireVersion),
	}
}

// externalArchive is the manifest of a's plugin when it is an external one.
func externalArchive(a config.ArchiveConfig) (config.ArchivePluginManifest, bool) {
	m, ok := config.ArchivePlugin(a.Plugin)
	return m, ok && m.External
}

// attachedExternalArchive is the attached archive when its plugin is an external one.
func (cluster *Cluster) attachedExternalArchive() (config.ArchiveConfig, bool) {
	name := cluster.Conf.AttachedArchive()
	if name == "" {
		return config.ArchiveConfig{}, false
	}
	list, _ := cluster.Conf.ArchiveList()
	for _, a := range list {
		if a.Name == name {
			_, ok := externalArchive(a)
			return a, ok
		}
	}
	return config.ArchiveConfig{}, false
}

// attachedArchiveRepoPath is the repository of the attached archive, the one a backup's
// snapshot was just stored in: an archive plugin's location, or the restic manager's
// repository. With an archive plugin attached no restic manager may run (backup-restic off).
func (cluster *Cluster) attachedArchiveRepoPath() string {
	if a, ok := cluster.attachedExternalArchive(); ok {
		return cluster.ArchiveLocation(a)
	}
	if cluster.ResticManager == nil {
		return ""
	}
	return cluster.ResticManager.GetRepoPath()
}

// archivePluginStore is the store an operation on a runs on, and the environment carrying its
// secrets (decrypted only into that environment) and, with the s3 capability, its provider's
// keys, short-lived ones for the Cloud18 SSO. envPrefix "" for the archive, "TARGET_" for a
// sync target.
func (cluster *Cluster) archivePluginStore(a config.ArchiveConfig, m config.ArchivePluginManifest, target bool) (wire.Store, []string, error) {
	st := wire.Store{Plugin: a.Plugin, Name: a.Name, Settings: map[string]string{}}
	var env []string
	for _, s := range m.Settings {
		v, ok := a.Settings[s.Name]
		if !ok || v == nil {
			continue
		}
		raw := fmt.Sprintf("%v", v)
		if s.Type == config.ConstArchiveSettingTypeSecret {
			name := s.Name
			if target {
				name = "target-" + name
			}
			env = append(env, wire.SecretEnv(name)+"="+cluster.Conf.GetDecryptedPassword(s.Name, raw))
			continue
		}
		if s.Name == config.ConstArchiveSettingS3Provider {
			continue
		}
		st.Settings[s.Name] = raw
	}
	if !m.Can(config.ConstArchiveCapS3) {
		return st, env, nil
	}
	name := fmt.Sprintf("%v", a.Settings[config.ConstArchiveSettingS3Provider])
	var provider *config.S3Provider
	for _, p := range cluster.GetS3ProvidersSnapshot() {
		if p.Name == name {
			p := p
			provider = &p
		}
	}
	if provider == nil {
		return st, nil, fmt.Errorf("archive %s: no S3 provider %q", a.Name, name)
	}
	if err := cluster.s3ProviderUsable(*provider); err != nil {
		return st, nil, err
	}
	endpoint, region, accessKey, secretKey := cluster.resolveProviderEffectiveValues(provider)
	st.Endpoint, st.Region = endpoint, region
	token, ssoUser := "", ""
	if provider.Cloud18SSO {
		creds, src, err := cluster.newCloud18SSOCredentials(endpoint, region, provider.RoleArn)
		if err != nil {
			return st, nil, err
		}
		v, err := creds.Get()
		if err != nil {
			return st, nil, err
		}
		accessKey, secretKey, token, ssoUser = v.AccessKeyID, v.SecretAccessKey, v.SessionToken, src.User()
	}
	st.Settings[config.ConstArchiveSettingS3Bucket], st.Settings[config.ConstArchiveSettingS3Prefix] = cluster.archiveBucketFolder(a, ssoUser)
	if target {
		env = append(env, wire.SecretEnv("target-s3-access-key")+"="+accessKey, wire.SecretEnv("target-s3-secret-key")+"="+secretKey, wire.SecretEnv("target-s3-session-token")+"="+token)
	} else {
		env = append(env, wire.EnvS3AccessKey+"="+accessKey, wire.EnvS3SecretKey+"="+secretKey, wire.EnvS3SessionToken+"="+token)
	}
	return st, env, nil
}

// runArchivePlugin runs one operation of a's external plugin and returns its response.
func (cluster *Cluster) runArchivePlugin(a config.ArchiveConfig, req wire.Request) (wire.Response, error) {
	m, ok := externalArchive(a)
	if !ok {
		return wire.Response{}, fmt.Errorf("archive %s: plugin %s is not an external plugin", a.Name, a.Plugin)
	}
	if err := cluster.Conf.ArchivePluginAllowed(a.Plugin); err != nil {
		return wire.Response{}, err
	}
	if err := cluster.archivePluginSigned(m.Binary); err != nil {
		return wire.Response{}, err
	}
	store, env, err := cluster.archivePluginStore(a, m, false)
	if err != nil {
		return wire.Response{}, err
	}
	req.Store = store
	if req.SyncTo != nil {
		// the sync target is given by name in SyncTo.Name
		list, _ := cluster.Conf.ArchiveList()
		var target *config.ArchiveConfig
		for i := range list {
			if list[i].Name == req.SyncTo.Name {
				target = &list[i]
			}
		}
		tm, ok := externalArchive(config.ArchiveConfig{Plugin: req.SyncTo.Plugin})
		if target == nil || !ok || tm.Binary != m.Binary {
			return wire.Response{}, fmt.Errorf("archive %s: sync target %s is not an archive of the same plugin program", a.Name, req.SyncTo.Name)
		}
		ts, tenv, err := cluster.archivePluginStore(*target, tm, true)
		if err != nil {
			return wire.Response{}, err
		}
		req.SyncTo = &ts
		env = append(env, tenv...)
	}
	timeout := archivePluginOpTimeout
	switch req.Op {
	case wire.OpPush, wire.OpRestore, wire.OpSync, wire.OpPurge, wire.OpRemove:
		timeout = archivePluginLongOpTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var in bytes.Buffer
	if err := wire.WriteRequest(&in, req); err != nil {
		return wire.Response{}, err
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, m.Binary)
	cmd.Env = append(archivePluginBaseEnv(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &stderr
	runErr := cmd.Run()
	tail := strings.TrimSpace(lastLines(stderr.String(), 5))
	if tail != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Archive %s %s: %s", a.Name, req.Op, tail)
	}
	// read before the answer: decoding drains out
	answered := out.Len() > 0
	resp, err := wire.ReadResponse(&out)
	if err != nil {
		if runErr != nil && !answered {
			// the plugin answered nothing: its last words are the only reason there is
			if tail != "" {
				return resp, fmt.Errorf("archive %s %s: %w: %s", a.Name, req.Op, runErr, tail)
			}
			return resp, fmt.Errorf("archive %s %s: %w", a.Name, req.Op, runErr)
		}
		return resp, fmt.Errorf("archive %s %s: %w", a.Name, req.Op, err)
	}
	return resp, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// archivePluginProbe opens a's archive, or creates it when missing.
func (cluster *Cluster) archivePluginProbe(a *config.ArchiveConfig, isNew bool) (string, error) {
	resp, err := cluster.runArchivePlugin(*a, wire.Request{Op: wire.OpProbe, Create: true, New: isNew})
	if err != nil {
		return "", err
	}
	if err := cluster.Conf.TakeArchivePluginSecrets(a, resp.Secrets); err != nil {
		return "", err
	}
	if resp.Created {
		return ArchiveProbeInitialised, nil
	}
	return ArchiveProbeOpened, nil
}

// archivePluginPolicy is the cluster's retention policy for a plugin.
func archivePluginPolicy(p config.ArchivePurgePolicy) *wire.Policy {
	return &wire.Policy{
		KeepLast: p.KeepLast, KeepHourly: p.KeepHourly, KeepDaily: p.KeepDaily, KeepWeekly: p.KeepWeekly,
		KeepMonthly: p.KeepMonthly, KeepYearly: p.KeepYearly, KeepWithin: p.KeepWithin,
		KeepWithinHourly: p.KeepWithinHourly, KeepWithinDaily: p.KeepWithinDaily, KeepWithinWeekly: p.KeepWithinWeekly,
		KeepWithinMonthly: p.KeepWithinMonthly, KeepWithinYearly: p.KeepWithinYearly,
	}
}

// pushExternalArchive archives backupPath into the attached external archive, records the
// snapshot in the backup's catalogue entry, applies Remove original and, after each backup,
// the retention policy; then clears the in-archive flag the caller set.
func (server *ServerMonitor) pushExternalArchive(a config.ArchiveConfig, backupMethod backupmgr.BackupMethod, meta *backupmgr.BackupMetadata, backupPath string, opt resticPushOptions, tags []string) {
	cluster := server.ClusterGroup
	done := func(err error) {
		if opt.done != nil {
			opt.done(err)
		}
	}
	defer func() {
		if opt.noFlag {
			return
		}
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(false)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(false)
		}
	}()
	if strings.TrimSpace(backupPath) == "" {
		backupPath = server.GetMyBackupDirectory()
	}
	resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpPush, Path: backupPath, Tags: tags, Host: server.URL})
	if err != nil || resp.Snapshot == nil {
		if err == nil {
			err = fmt.Errorf("no snapshot returned")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Archive %s: backup of %s not archived: %s", a.Name, server.URL, err)
		done(err)
		return
	}
	id := resp.Snapshot.ID
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: backup of %s archived, snapshot %s", a.Name, server.URL, shortID(id))
	if meta != nil {
		server.UpdateBackupMetadataWithRestic(backupMethod, meta, id)
		// the removal as the push says: backup-archive-remove-original, or asked with the
		// archive of this backup (ArchiveBackup)
		if opt.removeOriginal != nil && opt.removeOriginal() {
			server.removeArchivedOriginalAsked(backupMethod, meta, id, opt.asked)
		}
	}
	if opt.snapshot != nil {
		opt.snapshot(id)
	}
	if cluster.Conf.ArchivePurgeAfterBackup() && !opt.noPurge {
		cluster.purgeExternalArchive(a, false)
	}
	done(nil)
}

// ArchiveSnapshots lists the snapshots of the archive name, an archive plugin's (a restic
// archive's are listed by restic, /restic/snapshots), newest first. It asks the plugin, at
// most archivePluginOpTimeout: an archive in a bucket answers with one listing.
func (cluster *Cluster) ArchiveSnapshots(name string) ([]wire.Snapshot, error) {
	list, _ := cluster.Conf.ArchiveList()
	for _, a := range list {
		if a.Name != name {
			continue
		}
		if _, ok := externalArchive(a); !ok {
			return nil, fmt.Errorf("archive %s is a restic archive: restic lists its snapshots", name)
		}
		resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpList})
		if err != nil {
			return nil, err
		}
		snaps := resp.Snapshots
		if snaps == nil {
			snaps = []wire.Snapshot{}
		}
		sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
		return snaps, nil
	}
	return nil, fmt.Errorf("unknown archive %s", name)
}

// ForgetArchiveSnapshot deletes the snapshot id of the archive name, an archive plugin's (a
// restic archive's: /restic/purge/{snapshotID}); the backups recording it lose it
// (dropSnapshotFromCatalogue).
func (cluster *Cluster) ForgetArchiveSnapshot(name, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("archive %s: no snapshot given", name)
	}
	if err := cluster.snapbackPurgeHold(id); err != nil {
		return err
	}
	list, _ := cluster.Conf.ArchiveList()
	for _, a := range list {
		if a.Name != name {
			continue
		}
		if _, ok := externalArchive(a); !ok {
			return fmt.Errorf("archive %s is a restic archive: purge its snapshot with restic", name)
		}
		if _, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpForget, Snapshot: id}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Archive %s: snapshot %s not deleted: %s", a.Name, shortID(id), err)
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: snapshot %s deleted", a.Name, shortID(id))
		// the backups recording it have no copy there any more
		cluster.dropSnapshotFromCatalogue(a.Name, id)
		return nil
	}
	return fmt.Errorf("unknown archive %s", name)
}

// purgeExternalArchive asks a's plugin to apply its retention policy: a's own purge policies,
// else the cluster's. With tagged policies (#2091) replication-manager decides what stays, each
// policy counting its own snapshots, and asks the plugin to forget the others one by one.
func (cluster *Cluster) purgeExternalArchive(a config.ArchiveConfig, dryRun bool) error {
	rules, policy, err := cluster.Conf.ArchivePurgePolicies(&a)
	if err != nil {
		return err
	}
	if len(rules) > 0 {
		return cluster.purgeExternalArchiveByRules(a, rules, policy, dryRun)
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if policy.IsEmpty() {
		return fmt.Errorf("archive purge skipped: no keep-last/keep-within policy configured")
	}
	resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpPurge, Policy: archivePluginPolicy(policy), DryRun: dryRun})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Archive %s: purge failed: %s", a.Name, err)
		return err
	}
	if !dryRun {
		// the backups recording a snapshot the purge deleted lose it, as a forget (#2091)
		for _, id := range resp.Removed {
			cluster.dropSnapshotFromCatalogue(a.Name, id)
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: purge removed %d snapshot(s)", a.Name, len(resp.Removed))
	return nil
}

// purgeExternalArchiveByRules applies tagged purge policies to a plugin's archive: its
// snapshots listed, counted by host and paths, the ones no policy keeps forgotten. A plugin
// set not to purge (no-purge) is left alone.
func (cluster *Cluster) purgeExternalArchiveByRules(a config.ArchiveConfig, rules []config.ArchivePurgeRule, policy config.ArchivePurgePolicy, dryRun bool) error {
	if noPurge, _ := a.Settings["no-purge"].(bool); noPurge {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: purge skipped, no-purge is set", a.Name)
		return nil
	}
	resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpList})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Archive %s: purge failed, snapshots not listed: %s", a.Name, err)
		return err
	}
	snaps := make([]config.PurgeSnapshot, 0, len(resp.Snapshots))
	for _, s := range resp.Snapshots {
		paths := append([]string(nil), s.Paths...)
		sort.Strings(paths)
		snaps = append(snaps, config.PurgeSnapshot{ID: s.ID, Time: s.Time, Tags: s.Tags, Group: s.Host + "\x00" + strings.Join(paths, ",")})
	}
	kept := config.KeptByPolicies(snaps, rules, policy, nil)
	var forget []string
	for _, s := range snaps {
		if !kept[s.ID] {
			forget = append(forget, s.ID)
		}
	}
	verb := map[bool]string{true: "would delete", false: "deleting"}[dryRun]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: purge with policies %s and the default: keeping %d, %s %d of %d", a.Name, config.FormatArchivePurgeRules(rules), len(snaps)-len(forget), verb, len(forget), len(snaps))
	if dryRun {
		return nil
	}
	removed := 0
	for _, id := range forget {
		if _, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpForget, Snapshot: id}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Archive %s: snapshot %s not deleted by the purge: %s", a.Name, shortID(id), err)
			continue
		}
		cluster.dropSnapshotFromCatalogue(a.Name, id)
		removed++
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive %s: purge removed %d snapshot(s)", a.Name, removed)
	if removed < len(forget) {
		return fmt.Errorf("archive %s: %d of %d snapshot(s) not deleted", a.Name, len(forget)-removed, len(forget))
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
