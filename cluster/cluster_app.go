package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/viper"
)

var appTemplateRepoLocks sync.Map

func getAppTemplateRepoLock(key string) *sync.Mutex {
	mu, _ := appTemplateRepoLocks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func (cluster *Cluster) NewAppConfig(apphost, port string) *config.AppConfig {
	return &config.AppConfig{
		AppHost:           apphost,
		AppPort:           port,
		ProvAppDiskType:   "volume",
		ProvAppMem:        cluster.GetAppMemory(nil),
		ProvAppCpuCores:   cluster.GetAppCores(nil),
		ProvAppDisk:       cluster.GetAppDisk(nil),
		ProvAppAgents:     cluster.GetAppAgents(nil),
		ProvAppHATopology: cluster.GetAppHATopology(nil),
		Deployment:        config.NewDeploymentConfig(),
	}
}

func (cluster *Cluster) appendConfAppIfAbsent(appcnf *config.AppConfig) bool {
	if appcnf == nil {
		return false
	}
	cluster.Lock()
	defer cluster.Unlock()
	for _, cnf := range cluster.Conf.Apps {
		if cnf.AppHost == appcnf.AppHost && cnf.AppPort == appcnf.AppPort {
			return false
		}
	}
	cluster.Conf.Apps = append(cluster.Conf.Apps, appcnf)
	return true
}

func (cluster *Cluster) removeConfApp(appcnf *config.AppConfig, srv, port string) bool {
	cluster.Lock()
	defer cluster.Unlock()
	for i, cnf := range cluster.Conf.Apps {
		if cnf == appcnf || (cnf.AppHost == srv && cnf.AppPort == port) {
			cluster.Conf.Apps = append(cluster.Conf.Apps[:i], cluster.Conf.Apps[i+1:]...)
			return true
		}
	}
	return false
}

func (cluster *Cluster) LoadAppConfigs() error {
	dirname := filepath.Join(cluster.WorkingDir, "apps")

	// Check if the directory exists
	_, err := os.Stat(dirname)
	if os.IsNotExist(err) {
		// Create the directory if it does not exist
		err = os.MkdirAll(cluster.WorkingDir+"/apps", 0750)
		if err != nil {
			return err
		}
	}

	// Set the new configuration
	if cluster.Conf.Apps == nil {
		cluster.Conf.Apps = make([]*config.AppConfig, 0)
	}

	// Walk through the directory and load all the configuration files
	var firstErr error
	failedCount := 0
	walkErr := filepath.WalkDir(dirname, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".toml" {
			appname := strings.TrimSuffix(filepath.Base(path), ".toml")
			if loadErr := cluster.LoadAppConfig(dirname, appname); loadErr != nil {
				// ParseConfigMeasurement warnings are non-fatal and were historically ignored.
				var parseErrs config.ErrorConfigs
				if errors.As(loadErr, &parseErrs) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
						"App config %q loaded with measurement warnings: %v", path, loadErr)
					return nil
				}
				failedCount++
				if firstErr == nil {
					firstErr = loadErr
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
					"Failed to load app config %q: %v", path, loadErr)
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if failedCount > 0 {
		return fmt.Errorf("failed to load %d app config file(s): %w", failedCount, firstErr)
	}

	return nil
}

// GatewayConflict describes a single app that failed a gateway route conflict
// check.  Detection functions return this type; callers decide policy (log,
// abort startup, reject API write).  No detection function mutates Conf.Apps.
type GatewayConflict struct {
	AppHost string
	AppPort string
	Detail  string
}

// DetectIntraClusterGatewayConflicts detects route collisions between apps in
// this cluster that share the same gateway service.  It reports every conflict
// and returns a combined error, but does NOT mutate cluster.Conf.Apps or the
// live app list.
func (cluster *Cluster) DetectIntraClusterGatewayConflicts() ([]GatewayConflict, error) {
	gateway := strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService))
	if gateway == "" {
		return nil, nil
	}

	cluster.Lock()
	apps := make([]*config.AppConfig, len(cluster.Conf.Apps))
	copy(apps, cluster.Conf.Apps)
	cluster.Unlock()

	var conflicts []GatewayConflict
	var errs []error
	var acceptedRoutes [][]config.Route

	for _, appcnf := range apps {
		if appcnf == nil || appcnf.Deployment == nil {
			continue
		}
		normalized := config.NormalizedCopy(appcnf.Deployment.Routes)
		if len(normalized) == 0 {
			continue
		}

		var conflictDetails []string
		var acceptedFromApp []config.Route
		for _, route := range normalized {
			// Build priors = accepted from all prior apps + accepted so far from this app,
			// so routes within the same app are also checked against each other.
			priors := make([][]config.Route, len(acceptedRoutes))
			copy(priors, acceptedRoutes)
			if len(acceptedFromApp) > 0 {
				priors = append(priors, acceptedFromApp)
			}
			if err := config.CheckGatewayConflicts([]config.Route{route}, priors...); err != nil {
				lbl := route.Label()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
					"Intra-cluster gateway conflict for app %q route %s: %v — fix route config to resolve", appcnf.AppHost, lbl, err)
				conflictDetails = append(conflictDetails, "route "+lbl+": "+err.Error())
				errs = append(errs, fmt.Errorf("app %q (cluster %s) route %s: %w", appcnf.AppHost, cluster.Name, lbl, err))
			} else {
				acceptedFromApp = append(acceptedFromApp, route)
			}
		}
		if len(conflictDetails) > 0 {
			conflicts = append(conflicts, GatewayConflict{AppHost: appcnf.AppHost, AppPort: appcnf.AppPort, Detail: strings.Join(conflictDetails, "; ")})
		}
		if len(acceptedFromApp) > 0 {
			acceptedRoutes = append(acceptedRoutes, acceptedFromApp)
		}
	}

	return conflicts, errors.Join(errs...)
}

// DetectCrossClusterGatewayConflicts detects cross-cluster gateway route
// collisions against priorRoutes — the already-committed routes from clusters
// processed before this one in startup order.  It reports every conflict and
// returns a combined error, but does NOT mutate cluster.Conf.Apps or the live
// app list.
func (cluster *Cluster) DetectCrossClusterGatewayConflicts(priorRoutes [][]config.Route) ([]GatewayConflict, error) {
	gateway := strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService))
	if gateway == "" {
		return nil, nil
	}

	if len(priorRoutes) == 0 {
		return nil, nil
	}

	cluster.Lock()
	defer cluster.Unlock()

	var conflicts []GatewayConflict
	var errs []error

	for _, appcnf := range cluster.Conf.Apps {
		if appcnf == nil || appcnf.Deployment == nil {
			continue
		}
		normalized := config.NormalizedCopy(appcnf.Deployment.Routes)
		if len(normalized) == 0 {
			continue
		}

		var conflictDetails []string
		for _, route := range normalized {
			if err := config.CheckGatewayConflicts([]config.Route{route}, priorRoutes...); err != nil {
				lbl := route.Label()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
					"Cross-cluster gateway conflict for app %q route %s: %v — fix route config to re-enable", appcnf.AppHost, lbl, err)
				conflictDetails = append(conflictDetails, "route "+lbl+": "+err.Error())
				errs = append(errs, fmt.Errorf("app %q (cluster %s) route %s: %w", appcnf.AppHost, cluster.Name, lbl, err))
			}
		}
		if len(conflictDetails) > 0 {
			conflicts = append(conflicts, GatewayConflict{AppHost: appcnf.AppHost, AppPort: appcnf.AppPort, Detail: strings.Join(conflictDetails, "; ")})
		}
	}

	return conflicts, errors.Join(errs...)
}

// MarkGatewayConflicts stores detected gateway conflicts on the cluster and logs
// a WARN for each one.  The conflict set blocks gateway/OpenSVC publication for
// affected apps and surfaces the state through the monitoring loop.
func (cluster *Cluster) MarkGatewayConflicts(conflicts []GatewayConflict) {
	if len(conflicts) == 0 {
		return
	}
	cluster.Lock()
	// Copy-on-write: build a new map so callers that snapshotted the old pointer
	// under a prior lock can read it safely without holding the lock.
	size := len(conflicts)
	if cluster.GatewayConflicts != nil {
		size += len(cluster.GatewayConflicts)
	}
	next := make(map[string]string, size)
	for k, v := range cluster.GatewayConflicts {
		next[k] = v
	}
	for _, c := range conflicts {
		next[c.AppHost+":"+c.AppPort] = c.Detail
	}
	cluster.GatewayConflicts = next
	cluster.Unlock()
	for _, c := range conflicts {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
			"Gateway route conflict for app %q on cluster %s: %s — gateway routes blocked until config is fixed",
			c.AppHost, cluster.Name, c.Detail)
	}
}

// IsAppGatewayConflicted reports whether the app identified by host:port has a
// recorded gateway conflict.
func (cluster *Cluster) IsAppGatewayConflicted(host, port string) (bool, string) {
	cluster.Lock()
	defer cluster.Unlock()
	if cluster.GatewayConflicts == nil {
		return false, ""
	}
	reason, ok := cluster.GatewayConflicts[host+":"+port]
	return ok, reason
}

// ClearGatewayConflict removes the gateway-conflict marker for the given app.
// Call this when a route edit resolves the conflict so that the monitoring loop
// and OpenSVC publication unblock on the next cycle without requiring a restart.
func (cluster *Cluster) ClearGatewayConflict(host, port string) {
	cluster.Lock()
	// Copy-on-write: produce a new map without the cleared entry so that
	// concurrent readers holding the old snapshot are not affected.
	if cluster.GatewayConflicts != nil {
		key := host + ":" + port
		next := make(map[string]string, len(cluster.GatewayConflicts))
		for k, v := range cluster.GatewayConflicts {
			if k != key {
				next[k] = v
			}
		}
		cluster.GatewayConflicts = next
	}
	cluster.Unlock()
}

// RefreshGatewayConflicts re-runs intra-cluster conflict detection for this
// cluster and atomically replaces the GatewayConflicts map with a fresh
// intra-cluster snapshot.  Cross-cluster conflicts are then appended by the
// caller via MarkGatewayConflicts so that both types are cached, APPERR005
// fires for all blocked apps, and stale gateway fragments are withdrawn.
func (cluster *Cluster) RefreshGatewayConflicts() {
	gateway := strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService))
	if gateway == "" {
		cluster.Lock()
		cluster.GatewayConflicts = nil
		cluster.Unlock()
		return
	}

	intraConflicts, _ := cluster.DetectIntraClusterGatewayConflicts()

	fresh := make(map[string]string, len(intraConflicts))
	for _, c := range intraConflicts {
		fresh[c.AppHost+":"+c.AppPort] = c.Detail
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
			"Gateway route conflict (refreshed) for app %q: %s", c.AppHost, c.Detail)
	}

	cluster.Lock()
	cluster.GatewayConflicts = fresh
	cluster.Unlock()
}

// OwnGatewayRoutes returns the normalized routes contributed by this cluster's
// surviving apps, filtered to the given gateway service.  Returns nil when the
// cluster is not attached to that gateway.  Used by the startup loop to build
// the cumulative prior-routes slice in ClusterList order.
func (cluster *Cluster) OwnGatewayRoutes(gateway string) [][]config.Route {
	if strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService)) != gateway {
		return nil
	}
	return cluster.allAppRoutes()
}

// pruneEjectedAppsLocked removes entries from cluster.Apps that no longer have
// a corresponding AppConfig in cluster.Conf.Apps.  Caller must hold the cluster
// lock.  Rebuilds AppS3Providers and bumps appListEpoch.
func (cluster *Cluster) pruneEjectedAppsLocked() {
	accepted := make(map[*config.AppConfig]bool, len(cluster.Conf.Apps))
	for _, appcnf := range cluster.Conf.Apps {
		accepted[appcnf] = true
	}
	filtered := make([]*App, 0, len(cluster.Apps))
	newS3Providers := make([]string, 0)
	for _, app := range cluster.Apps {
		if app == nil || app.AppConfig == nil || !accepted[app.AppConfig] {
			continue
		}
		filtered = append(filtered, app)
		if app.AppConfig.AppS3Provider {
			hostport := app.GetHost() + ":" + app.GetPort()
			newS3Providers = append(newS3Providers, hostport)
		}
	}
	cluster.Apps = filtered
	cluster.AppS3Providers = newS3Providers
	cluster.bumpAppListVersion()
}

// pruneEjectedAppsFromLiveList acquires the cluster lock and delegates to
// pruneEjectedAppsLocked.  Use this from callers that do not already hold the
// lock; use pruneEjectedAppsLocked directly when the lock is already held.
func (cluster *Cluster) pruneEjectedAppsFromLiveList() {
	cluster.Lock()
	cluster.pruneEjectedAppsLocked()
	cluster.Unlock()
}

// allAppRoutes returns a normalized route slice per app for this cluster,
// excluding apps that have a cached gateway conflict (their routes are blocked
// from publication and must not occupy gateway address space for peer clusters).
// Routes are read via GetDeploymentRoutesSnapshot so API mutations don't race
// with the copy.
func (cluster *Cluster) allAppRoutes() [][]config.Route {
	// Snapshot conflicts and the live app list together under one lock so we
	// don't race with MarkGatewayConflicts or app-list updates.
	cluster.Lock()
	conflicts := cluster.GatewayConflicts
	apps := make([]*App, len(cluster.Apps))
	copy(apps, cluster.Apps)
	cluster.Unlock()

	var result [][]config.Route
	for _, app := range apps {
		if app == nil || app.AppConfig == nil {
			continue
		}
		if _, conflicted := conflicts[app.AppConfig.AppHost+":"+app.AppConfig.AppPort]; conflicted {
			continue
		}
		// GetDeploymentRoutesSnapshot holds the app lock while copying routes,
		// preventing a race with concurrent API route mutations.
		if normalized := config.NormalizedCopy(app.GetDeploymentRoutesSnapshot()); len(normalized) > 0 {
			result = append(result, normalized)
		}
	}
	return result
}

// GetAppsCopy returns a shallow copy of the Apps slice taken under the cluster
// lock.  Use this in the server package to iterate apps without holding the
// cluster lock for the duration of the loop.
func (cluster *Cluster) GetAppsCopy() []*App {
	cluster.Lock()
	defer cluster.Unlock()
	if len(cluster.Apps) == 0 {
		return nil
	}
	cp := make([]*App, len(cluster.Apps))
	copy(cp, cluster.Apps)
	return cp
}

// WithdrawConflictedGatewayRoutes removes any HAProxy fragments that were
// previously published for apps now flagged with a gateway conflict.  Called
// after MarkGatewayConflicts / RefreshGatewayConflicts so that stale fragments
// left over from a prior (valid) run are cleaned up without waiting for the
// next OpenSVCProvisionRoute cycle.  Best-effort: errors are logged as warnings.
func (cluster *Cluster) WithdrawConflictedGatewayRoutes() {
	if strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService)) == "" {
		return
	}
	cluster.Lock()
	conflicts := cluster.GatewayConflicts
	apps := cluster.Apps
	cluster.Unlock()
	if len(conflicts) == 0 {
		return
	}
	for _, app := range apps {
		if app == nil || app.AppConfig == nil {
			continue
		}
		if _, conflicted := conflicts[app.AppConfig.AppHost+":"+app.AppConfig.AppPort]; !conflicted {
			continue
		}
		if err := cluster.withdrawGatewayRoutes(app); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Failed to withdraw stale gateway routes for conflicted app %q: %v", app.Name, err)
		}
	}
}

// LoadConfig loads the configuration from a file to the configuration struct.
// If the file does not exist, it will return an error.
// If the file exists but cannot be read, it will return the old configuration and the error.
func (cluster *Cluster) LoadAppConfig(dirname, appname string) error {

	// Create a new configuration struct
	var appcnf config.AppConfig
	appcnf.Deployment = config.NewDeploymentConfig()

	filename := filepath.Join(dirname, appname+".toml")

	// Load the configuration file
	_, err := os.Stat(filename)
	if err != nil {
		return err
	}

	rawContent, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	canonicalContent, canonicalRes, err := config.CanonicalizeAppTemplateTOML(rawContent)
	if err != nil {
		return err
	}

	// Open TOML file
	appViper := viper.New()
	appViper.SetConfigType("toml")
	err = appViper.ReadConfig(bytes.NewBuffer(canonicalContent))
	if err != nil {
		// If there is an error reading the TOML file don't change the configuration
		return err
	}

	// Decode TOML file into the configuration struct
	err = appViper.Unmarshal(&appcnf)
	if err != nil {
		// If there is an error decoding the TOML file don't change the configuration
		return err
	}

	var storageMigrated, storageNormalized, shadowsRepaired bool
	if appcnf.Deployment != nil {
		if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
			for _, resolveErr := range resolveErrs {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
					"App config %q deployment path resolution error: %v", filename, resolveErr)
			}
			return fmt.Errorf("invalid deployment path mapping in app config %q", filename)
		}
		// Auto-migrate legacy storage model to canonical v2 on every load, and
		// normalize any canonical mount paths that aren't already in cleaned
		// form. Both are persisted below (alongside template canonicalization)
		// so the migration runs at most once per config rather than on every restart.
		defaultSize := appcnf.ProvAppDisk
		var migrateErr error
		storageMigrated, migrateErr = config.EnsureCanonicalStorage(appcnf.Deployment, defaultSize)
		if migrateErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
				"App config %q storage auto-migration failed (legacy config preserved): %v", filename, migrateErr)
		}
		// For already-canonical configs (v2 on disk), EnsureCanonicalStorage is a
		// no-op.  Run an explicit validation pass — on the as-loaded (not yet
		// normalized) values — so malformed canonical configs are caught at load
		// time rather than silently failing later during provisioning.
		//
		// Validation MUST run before NormalizeCanonicalStorage: normalization uses
		// filepath.Clean, which lexically resolves ".." segments (e.g.
		// "/srv/../../etc" -> "/etc"). If normalization ran first, a forbidden
		// traversal in the raw value would be silently rewritten into something
		// that passes the ".." check, defeating it.
		if appcnf.Deployment.IsCanonical() {
			if valErr := appcnf.Deployment.ValidateCanonicalStorage(); valErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
					"App config %q canonical storage is invalid: %v — fix config before restarting", filename, valErr)
				return fmt.Errorf("invalid canonical storage in app config %q: %w", filename, valErr)
			}
		}
		storageNormalized = appcnf.Deployment.NormalizeCanonicalStorage()
		// Hand-authored or previously-trimmed v2 TOMLs can carry a canonical model
		// (AppVolumes/AppSources/AppMounts) whose legacy Storages.*/Paths shadows —
		// still relied on by older readers (UI display endpoints, GetGitClone,
		// GetS3Mount) — are missing or undercounted relative to it. Detection is
		// deliberately count-based rather than content-based: MigrateStorageToCanonical
		// preserves original legacy values as-is (richer than SyncLegacyShadows can
		// regenerate), so a content comparison would misreport legitimate preserved
		// data as "stale" and flatten it on every load — see HasMissingLegacyShadows.
		// Skipped when normalization already triggered a full SyncLegacyShadows below.
		if !storageNormalized && appcnf.Deployment.HasMissingLegacyShadows() {
			appcnf.Deployment.SyncLegacyShadows()
			shadowsRepaired = true
		}
		// Normalize routes eagerly so in-memory state always has canonical
		// mode/sourcePort/destPort values, regardless of how old the TOML is.
		appcnf.Deployment.NormalizeRoutes()
		if err := appcnf.Deployment.ValidateRoutes(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
				"App config %q has invalid routes: %v — fix route config before restarting", filename, err)
			return fmt.Errorf("invalid routes in app config %q: %w", filename, err)
		}
		for _, route := range appcnf.Deployment.Routes {
			if route.Monitor == nil || route.Monitor.AuthType == "" {
				continue
			}
			if route.Protocol == "http" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
					"App config %q: monitor auth (%s) configured on plain HTTP route %s — credentials will be transmitted unencrypted",
					filename, route.Monitor.AuthType, route.CName)
			} else if route.Protocol == "https" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
					"App config %q: monitor auth (%s) configured on HTTPS route %s with unverified certificate (InsecureSkipVerify=true)",
					filename, route.Monitor.AuthType, route.CName)
			}
		}
	}

	if storageMigrated || storageNormalized || shadowsRepaired {
		if storageNormalized {
			// Normalization rewrote AppMounts target/source-sub paths in place.
			// Any previously-synced legacy Storages.*/Paths shadow now points at
			// the stale, un-normalized values, so it must be regenerated from the
			// canonical model before we persist — canonicalStorageSave does the
			// same after CRUD edits (see SyncLegacyShadows in deployment.go).
			//
			// This is NOT needed for a fresh migration: MigrateStorageToCanonical
			// derives the canonical model FROM the legacy fields and intentionally
			// preserves them as-is, so they are already consistent immediately
			// after migration — regenerating them here would flatten richer
			// preserved data (e.g. parent/child path hierarchies) for no benefit.
			appcnf.Deployment.SyncLegacyShadows()
		}
		// Storage migration/normalization mutated the in-memory deployment, so
		// the persisted form must come from re-marshalling the struct rather
		// than the raw (pre-migration) canonicalContent bytes. Persisting here
		// ensures the migration runs at most once per config.
		marshalled, err := toml.Marshal(&appcnf)
		if err != nil {
			return err
		}
		t, err := toml.LoadBytes(marshalled)
		if err != nil {
			return err
		}
		if err := cluster.writeTomlAtomically(t, filename); err != nil {
			return err
		}
		reason := "migration/normalization"
		if shadowsRepaired && !storageMigrated && !storageNormalized {
			reason = "legacy shadow repair"
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"Persisted canonical storage %s for app config %q", reason, filename)
	} else if canonicalRes.Changed {
		t, err := toml.LoadBytes(canonicalContent)
		if err != nil {
			return err
		}
		if err := cluster.writeTomlAtomically(t, filename); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"Canonicalized legacy app config template in %q", filename)
	}

	// If app-host was not set in the TOML file (or was left as an unresolved template),
	// fall back to the file name so the app gets a valid, stable Name and ID.
	if appcnf.AppHost == "" || strings.Contains(appcnf.AppHost, "{{") {
		appcnf.AppHost = appname
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"App config %q had no resolved app-host; using filename as host: %s", filename, appname)
	}
	if appcnf.AppPort == "" {
		appcnf.AppPort = "80"
	}

	// Skip duplicate entries (same host+port already loaded, e.g. from main config).
	for _, existing := range cluster.Conf.Apps {
		if existing.AppHost == appcnf.AppHost && existing.AppPort == appcnf.AppPort {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg,
				"App config %s:%s already loaded, skipping duplicate from %q", appcnf.AppHost, appcnf.AppPort, filename)
			return nil
		}
	}

	cluster.Conf.Apps = append(cluster.Conf.Apps, &appcnf)

	errormap := config.ParseConfigMeasurement(&appcnf, cluster.Conf.DefaultFlagMap, cluster.Conf.MeasurementAutoClampLimit)
	if len(errormap) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error parsing app config %s: %v", appname, errormap)
	}

	return errormap
}

// // LoadConfig loads the configuration from a file to the configuration struct.
// // If the file does not exist, it will return an error.
// // If the file exists but cannot be read, it will return the old configuration and the error.
// func (cluster *Cluster) LoadDeploymentsConfig(dirpath, appname string, appcnf *config.AppConfig) error {

// 	// Create a new configuration struct
// 	var result config.Deployment
// 	dirname := filepath.Join(dirpath, appname)
// 	if _, err := os.Stat(dirname); os.IsNotExist(err) {
// 		os.MkdirAll(dirname, os.ModePerm)
// 	}

// 	filename := filepath.Join(dirpath, appname, "deployments.toml")

// 	// Load the configuration file
// 	_, err := os.Stat(filename)
// 	if err != nil {
// 		return err
// 	}

// 	// Open TOML file
// 	appViper := viper.New()
// 	appViper.SetConfigFile(filename)
// 	err = appViper.ReadInConfig()
// 	if err != nil {
// 		// If there is an error reading the TOML file don't change the configuration
// 		return err
// 	}

// 	// Decode TOML file into the configuration struct
// 	err = appViper.Unmarshal(&result)
// 	if err != nil {
// 		// If there is an error decoding the TOML file don't change the configuration
// 		return err
// 	}

// 	// Set the new configuration
// 	appcnf.Deployments = result

// 	for _, dep := range appcnf.Deployments {
// 		if dep.Variables == nil {
// 			dep.Variables = make([]config.VariableMapping, 0)
// 		}
// 		if dep.Path == nil {
// 			dep.Path = make([]config.PathMapping, 0)
// 		}
// 		if dep.Routes == nil {
// 			dep.Routes = make([]config.Route, 0)
// 		}
// 		if dep.GitClones == nil {
// 			dep.GitClones = make([]config.GitClone, 0)
// 		}
// 	}

// 	return nil
// }

func (cluster *Cluster) SaveAppConfigs() (bool, error) {
	var has_changed bool
	for _, app := range cluster.Apps {
		changed, err := cluster.SaveApp(app, "")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error saving app %s: %s", app.Name, err)
			// return err
		}

		if changed {
			has_changed = true
		}
	}
	return has_changed, nil
}

func (cluster *Cluster) SaveApp(app *App, templatePath string) (bool, error) {
	var has_changed bool
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	filePath := cluster.WorkingDir + "/apps/" + app.Name + ".toml"

	// Save the main configuration file
	changed, err := cluster.SaveAppConfigFile(app, filePath, templatePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save app config: %s", err)
		return false, err
	}

	if changed {
		has_changed = true
	}

	return has_changed, nil
}

func (cluster *Cluster) SaveAppConfigFile(app *App, filePath, templatePath string) (bool, error) {

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(app.AppConfig)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
		return false, err
	}

	// Load TOML and sort keys
	t, err := toml.LoadBytes(readconf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
		return false, err
	}

	if err := cluster.writeTomlAtomically(t, filePath); err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing app file atomically: %s", err)
		}
		return false, err
	}

	if templatePath != "" {
		parentDir := filepath.Dir(templatePath)
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			if err := os.MkdirAll(parentDir, 0750); err != nil {
				return false, err
			}
		}
		if err := cluster.writeTomlAtomically(t, templatePath); err != nil {
			if os.IsPermission(err) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", templatePath)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing template file atomically: %s", err)
			}
			return false, err
		}
	}

	return true, nil
}

// writeTomlAtomically writes a TOML tree via temp-file + fsync + rename to avoid
// truncating a target file on partial writes.
func (cluster *Cluster) writeTomlAtomically(t *toml.Tree, filePath string) error {
	parentDir := filepath.Dir(filePath)
	// 0750: owner rwx, group rx, other none — more restrictive than os.ModePerm
	// (0777) to protect config dirs that may hold database credentials.
	if err := os.MkdirAll(parentDir, 0750); err != nil {
		return err
	}

	perm := os.FileMode(0640)
	if fi, err := os.Stat(filePath); err == nil {
		perm = fi.Mode()
	}

	tmpFile, err := os.CreateTemp(parentDir, ".repman-toml-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := t.WriteTo(tmpFile); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}

	if dir, err := os.Open(parentDir); err == nil {
		if syncErr := dir.Sync(); syncErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"writeTomlAtomically: directory fsync failed for %s: %v (rename durability not guaranteed)", parentDir, syncErr)
		}
		if closeErr := dir.Close(); closeErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"writeTomlAtomically: directory close failed for %s: %v", parentDir, closeErr)
		}
	}

	return nil
}

// func (cluster *Cluster) SaveAppDeploymentsFile(app *App) (bool, error) {
// 	filePath := app.Datadir + "/deployments.toml"

// 	// Write sorted values to file
// 	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
// 	if err != nil {
// 		if os.IsPermission(err) {
// 			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
// 		} else {
// 			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
// 		}
// 		return false, err
// 	}
// 	defer file.Close()

// 	// Marshal and write TOML configuration
// 	readconf, err := toml.Marshal(app.GetAppConfig().Deployments)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
// 		return false, err
// 	}

// 	// Load TOML and sort keys
// 	t, err := toml.LoadBytes(readconf)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
// 		return false, err
// 	}

// 	_, err = t.WriteTo(file)
// 	if err != nil {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing toml: %s", err)
// 		return false, err
// 	}

// 	return true, nil
// }

func (cluster *Cluster) AddSeededApp(srv, port, dockerImg, template string) error {
	var newViper *viper.Viper
	var content []byte
	var err error

	srv = strings.TrimSpace(srv)
	port = strings.TrimSpace(port)
	dockerImg = strings.TrimSpace(dockerImg)
	template = strings.TrimSpace(template)

	if srv == "" {
		return errors.New("app host is required")
	}

	if dockerImg == "" && template == "" {
		return errors.New("docker image or template is required")
	}

	if template != "" {
		content, err = cluster.GetTemplateContent(template)
		if err != nil {
			return err
		}

		newViper, err = cluster.LoadTemplateToViper(content)
		if err != nil {
			return err
		}

		if port == "" || port == "0" {
			port = newViper.GetString("app-port")
			if port == "" || port == "0" {
				port = "80"
			}
		}

		if dockerImg == "" {
			dockerImg = newViper.GetString("prov-app-docker-img")
			if dockerImg == "" {
				return errors.New("Docker image is required in the template")
			}
		}
	}

	if port == "" || port == "0" {
		return errors.New("app port is required")
	}

	portNumber, convErr := strconv.Atoi(port)
	if convErr != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("app port must be between 1 and 65535")
	}

	appcnf := cluster.NewAppConfig(srv, port)
	appcnf.ProvAppDockerImg = dockerImg
	appcnf.ProvAppTemplate = template
	if appended := cluster.appendConfAppIfAbsent(appcnf); !appended {
		return errors.New("App already exists. If you want to add new deployment, please use the app deployment menu")
	}

	appAdded := true
	rollbackAddedApp := func() {
		if !appAdded {
			return
		}
		_ = cluster.removeConfApp(appcnf, srv, port)
		_ = cluster.newAppList()
		appAdded = false
	}

	if err := cluster.newAppList(); err != nil {
		rollbackAddedApp()
		return err
	}

	app := cluster.GetAppByConfig(appcnf)
	if app == nil {
		rollbackAddedApp()
		return fmt.Errorf("failed to create app object for %s:%s", srv, port)
	}
	app.CheckPrimaryRoute()

	if template != "" {
		resolvedContent, err := cluster.ParseTemplateContent(app, content)
		if err != nil {
			rollbackAddedApp()
			return err
		}

		canonicalContent, _, err := config.CanonicalizeAppTemplateTOML(resolvedContent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Error canonicalizing parsed template content for %s: %s", template, err)
			rollbackAddedApp()
			return err
		}

		newViper, err = cluster.LoadTemplateToViper(canonicalContent)
		if err != nil {
			rollbackAddedApp()
			return err
		}
		newViper.Set("app-host", srv)
		newViper.Set("app-port", port)
		newViper.Set("prov-app-docker-img", dockerImg)
		newViper.Set("prov-app-template", template)

		// Unmarshal the parsed content into the app configuration
		err = newViper.Unmarshal(appcnf)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error unmarshalling parsed template file %s: %s", template, err)
			rollbackAddedApp()
			return err
		}

		if appcnf.Deployment != nil {
			if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
				for _, resolveErr := range resolveErrs {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
						"App template %q deployment path resolution error: %v", template, resolveErr)
				}
				rollbackAddedApp()
				return fmt.Errorf("invalid deployment path mapping for template %q", template)
			}
			appcnf.Deployment.NormalizeRoutes()
			if err := appcnf.Deployment.ValidateRoutes(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
					"App template %q has invalid routes: %v", template, err)
				rollbackAddedApp()
				return fmt.Errorf("invalid routes in template %q: %w", template, err)
			}
		}
	}
	appAdded = false
	return nil
}

func (cluster *Cluster) GetAppByHostPort(host, port string) (*App, int) {
	// Check if the app exists in the cluster
	for i, app := range cluster.Apps {
		if app.GetHost() == host && app.GetPort() == port {
			return app, i // Return the existing app and its index
		}
	}

	return nil, -1
}

func (cluster *Cluster) GetAppByConfig(appcnf *config.AppConfig) *App {
	// Check if the app exists in the cluster
	for _, app := range cluster.Apps {
		if app.AppConfig != nil && app.AppConfig.AppHost == appcnf.AppHost && app.AppConfig.AppPort == appcnf.AppPort {
			return app
		}
	}

	return nil
}

func (cluster *Cluster) GetAppAgents(appcnf *config.AppConfig) string {
	if appcnf != nil {
		// Get the app config
		if appcnf.ProvAppAgents != "" {
			// If the app config has agents, return them
			return appcnf.ProvAppAgents
		}
	}

	// If the app config does not have agents, return the cluster agents
	agents := cluster.Conf.ProvAppAgents
	if agents == "" {
		// If the cluster does not have agents, return the default agents
		agents = cluster.Conf.ProvAgents
	}

	if agents != "" && appcnf != nil {
		appcnf.ProvAppAgents = agents
	}

	return agents
}

func (cluster *Cluster) GetAppDisk(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppDisk != "" {
		// If the app config has disk, return it
		return appcnf.ProvAppDisk
	}

	// If the app config does not have disk, return the cluster disk
	disk := cluster.Conf.ProvAppDisk
	if disk == "" {
		// If the cluster does not have disk, return the default disk
		disk = cluster.Conf.ProvDisk
	}

	if disk != "" && appcnf != nil {
		appcnf.ProvAppDisk = disk
	}

	return disk
}

func (cluster *Cluster) GetAppMemory(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppMem != "" {
		// If the app config has memory, return it
		return appcnf.ProvAppMem
	}

	// If the app config does not have memory, return the cluster memory
	mem := cluster.Conf.ProvAppMem
	if mem == "" {
		// If the cluster does not have memory, return the default memory
		mem = cluster.Conf.ProvMem
	}

	if mem != "" && appcnf != nil {
		appcnf.ProvAppMem = mem
	}

	return mem
}

// GetAppCores returns the cores for the app.
func (cluster *Cluster) GetAppCores(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppCpuCores != "" {
		// If the app config has cores, return it
		return appcnf.ProvAppCpuCores
	}

	// If the app config does not have cores, return the cluster cores
	cores := cluster.Conf.ProvAppCpuCores
	if cores == "" {
		// If the cluster does not have cores, return the default cores
		cores = cluster.Conf.ProvCores
	}

	if cores != "" && appcnf != nil {
		appcnf.ProvAppCpuCores = cores
	}

	return cores
}

func (cluster *Cluster) GetAppHATopology(appcnf *config.AppConfig) string {
	if appcnf != nil && appcnf.ProvAppHATopology != "" {
		// If the app config has HA topology, return it
		return appcnf.ProvAppHATopology
	}

	// If the app config does not have HA topology, return the cluster HA topology
	haTopology := cluster.Conf.ProvAppHATopology
	if haTopology != "" && appcnf != nil {
		appcnf.ProvAppHATopology = haTopology
	}

	return haTopology
}

func (cluster *Cluster) refreshApps(wg *sync.WaitGroup) {
	defer wg.Done()

	var workerWg sync.WaitGroup
	appChan := make(chan *App)

	workerCount := cluster.Conf.AppRefreshConcurrency
	if workerCount <= 0 {
		workerCount = 1
	}

	for range workerCount {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for app := range appChan {
				app.Refresh()
			}
		}()
	}

	for _, app := range cluster.Apps {
		if app != nil {
			appChan <- app
		}
	}
	close(appChan)

	workerWg.Wait()
}

func (cluster *Cluster) EmitAppErrors() {
	for _, app := range cluster.Apps {
		if app == nil {
			continue
		}
		app.Lock()
		for key, st := range app.ErrState {
			if st.ErrKey == "" {
				st.ErrKey = key
			}

			cluster.SetState(key, st)
		}
		app.Unlock()
	}
}

func (cluster *Cluster) GetAppByURL(url string) (*App, int) {
	var host, port string
	newURL := strings.TrimSpace(url)
	if newURL == "" {
		return nil, -1 // Return nil if the URL is empty
	}

	// Split the URL and strip the protocol if present
	if strings.Contains(newURL, "://") {
		parts := strings.SplitN(newURL, "://", 2)
		if len(parts) == 2 {
			newURL = parts[1] // Use the part after the protocol
		}
	}

	// Split the URL into host and port
	parts := strings.SplitN(newURL, ":", 2)
	if len(parts) == 2 {
		host = parts[0]
		port = parts[1]
	} else {
		host = parts[0]
		port = "80" // Default port if not specified
	}

	return cluster.GetAppByHostPort(host, port)
}

func (cluster *Cluster) GetAppDecryptedVariableValue(app *App, key string) (string, error) {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return "", errors.New("app or app configuration is not initialized")
	}

	for _, variable := range app.AppConfig.Deployment.Variables {
		if variable.Name == key {
			return cluster.Conf.GetDecryptedPassword(key, variable.Value), nil
		}
	}

	return "", errors.New("variable not found")
}

func (cluster *Cluster) GetAppEncryptedVariableValue(app *App, key string) (string, error) {
	decrypted, err := cluster.GetAppDecryptedVariableValue(app, key)
	if err != nil {
		return "", err
	}

	return cluster.Conf.GetEncryptedString(decrypted), nil
}

func (cluster *Cluster) SetAppVariableValue(app *App, v config.VariableMapping) error {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return errors.New("app or app configuration is not initialized")
	}
	newValue := v.Value
	if v.Type == config.VariableTypeSecret {
		newValue = cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedPassword(v.Name, v.Value))
	}

	for i, variable := range app.AppConfig.Deployment.Variables {
		if variable.Name == v.Name {
			app.AppConfig.Deployment.Variables[i].Value = newValue
			app.AppConfig.Deployment.Variables[i].Type = v.Type
			return nil
		}
	}

	// If the variable does not exist, add it — preserve all fields from the input
	newVar := v
	newVar.Value = newValue
	app.AppConfig.Deployment.Variables = append(app.AppConfig.Deployment.Variables, newVar)
	return nil
}

func normalizeTemplateIdentifier(template string) (string, error) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", fmt.Errorf("template name must be provided")
	}

	cleanTemplateName := filepath.Clean(filepath.FromSlash(trimmed))
	if cleanTemplateName == "." || cleanTemplateName == string(filepath.Separator) {
		return "", fmt.Errorf("template name must be provided")
	}
	if filepath.IsAbs(cleanTemplateName) {
		return "", fmt.Errorf("template name must be relative to templates root")
	}

	relFromRoot, err := filepath.Rel(".", cleanTemplateName)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relFromRoot == ".." || strings.HasPrefix(relFromRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}

	return filepath.ToSlash(cleanTemplateName), nil
}

// GlobalTemplatesRoot returns the global templates directory shared across all clusters.
func GlobalTemplatesRoot(workingDir string) string {
	return filepath.Join(workingDir, ".templates", "apps")
}

// TemplateRepoCacheRoot returns the pull-only app template repository cache root.
func TemplateRepoCacheRoot(workingDir string) string {
	return filepath.Join(workingDir, ".templates", "repos", "apps")
}

// ClusterTemplatesRoot returns the cluster-specific templates directory.
func (cluster *Cluster) ClusterTemplatesRoot() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, ".templates", "apps")
}

func buildTemplateAbsPath(root, normalizedName string) (string, error) {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("invalid templates root path: %w", err)
	}
	pathAbs, err := filepath.Abs(filepath.Clean(filepath.Join(rootAbs, filepath.FromSlash(normalizedName)+".toml")))
	if err != nil {
		return "", fmt.Errorf("invalid template path: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}
	return pathAbs, nil
}

// resolveTemplateLocalCachePath returns the canonical write path for a template.
// Cache writes (from embedded/repo) always go to the global root.
func (cluster *Cluster) resolveTemplateLocalCachePath(template string) (string, string, error) {
	normalizedTemplateName, err := normalizeTemplateIdentifier(template)
	if err != nil {
		return "", "", err
	}

	localPathAbs, err := buildTemplateAbsPath(GlobalTemplatesRoot(cluster.Conf.WorkingDir), normalizedTemplateName)
	if err != nil {
		return "", "", err
	}
	return normalizedTemplateName, localPathAbs, nil
}

// resolveTemplateReadPaths returns paths to check in order: cluster-specific first, then global.
func (cluster *Cluster) resolveTemplateReadPaths(normalizedName string) []string {
	clusterPath, _ := buildTemplateAbsPath(cluster.ClusterTemplatesRoot(), normalizedName)
	globalPath, _ := buildTemplateAbsPath(GlobalTemplatesRoot(cluster.Conf.WorkingDir), normalizedName)
	return []string{clusterPath, globalPath}
}

func resolveSharedTemplateRepoPath(normalizedTemplate string) (string, error) {
	templateName := normalizedTemplate
	if strings.HasPrefix(templateName, "shared/") {
		templateName = strings.TrimPrefix(templateName, "shared/")
	}
	cleanName := filepath.Clean(filepath.FromSlash(templateName))
	if cleanName == "." || cleanName == string(filepath.Separator) {
		return "", fmt.Errorf("template name must include a path")
	}
	relFromRoot, err := filepath.Rel(".", cleanName)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	relFromRoot = filepath.ToSlash(relFromRoot)
	if relFromRoot == ".." || strings.HasPrefix(relFromRoot, "../") {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}
	return filepath.ToSlash(filepath.Join("app", "templates", filepath.ToSlash(cleanName)+".toml")), nil
}

func IsSharedDummyTemplate(normalizedTemplate string) bool {
	return strings.TrimSpace(normalizedTemplate) == "shared/dummy"
}

func (cluster *Cluster) loadLocalTemplate(path, template string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error reading local template file %s: %s", template, err)
	}
	return content, err
}

func (cluster *Cluster) readTemplateFromRepoCache(repoDir, template string) ([]byte, error) {
	templatePath, err := buildTemplateAbsPath(repoDir, template)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(templatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("error reading template from repo cache: %w", err)
	}
	return content, nil
}

func (cluster *Cluster) loadTemplateFromRepo(template string, forceRefresh bool) ([]byte, error) {
	repoDir, err := cluster.Conf.ResolveAppTemplateRepoCacheDir()
	if err != nil {
		return nil, err
	}
	lock := getAppTemplateRepoLock(repoDir)
	lock.Lock()
	defer lock.Unlock()

	_, readErrBeforeSync := cluster.readTemplateFromRepoCache(repoDir, template)
	hasStaleCopy := readErrBeforeSync == nil

	if forceRefresh || !hasStaleCopy {
		if _, syncErr := cluster.Conf.SyncAppTemplateRepoCache(forceRefresh); syncErr != nil {
			if hasStaleCopy {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
					"Unable to refresh template repo cache, serving stale template %s: %s", template, syncErr)
			} else {
				return nil, syncErr
			}
		}
	}

	content, err := cluster.readTemplateFromRepoCache(repoDir, template)
	if err != nil {
		return nil, err
	}

	return content, nil
}

func (cluster *Cluster) loadTemplateFromShared(template string) ([]byte, error) {
	if !IsSharedDummyTemplate(template) {
		return nil, fmt.Errorf("shared fallback is only available for shared/dummy")
	}

	templatePath, err := resolveSharedTemplateRepoPath(template)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error validating shared template path %s: %s", template, err)
		return nil, err
	}

	content, err := share.ReadFileFromSharedDir(
		cluster.Conf.WithEmbed,
		cluster.Conf.ShareDir,
		templatePath,
	)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error reading template file %s: %s", templatePath, err)
		return nil, err
	}
	return content, nil
}

func (cluster *Cluster) GetTemplateContent(template string) ([]byte, error) {
	return cluster.getTemplateContent(template, false)
}

// RefreshTemplateContent bypasses local cache and refreshes template content
// from repo/shared source, then overwrites local cache with validated canonical
// content.
func (cluster *Cluster) RefreshTemplateContent(template string) ([]byte, error) {
	return cluster.getTemplateContent(template, true)
}

func (cluster *Cluster) loadAndValidateLocalTemplate(path, normalizedName string, rewriteOnChange bool) ([]byte, error) {
	content, err := cluster.loadLocalTemplate(path, normalizedName)
	if err != nil {
		return nil, err
	}
	canonicalContent, canonicalRes, canonErr := config.CanonicalizeAppTemplateTOML(content)
	if canonErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error canonicalizing local template file %s: %s", path, canonErr)
		return nil, canonErr
	}
	if err := cluster.validateTemplateDeploymentPaths(canonicalContent, normalizedName); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Invalid local template file %s after canonicalization: %s", path, err)
		return nil, err
	}
	if rewriteOnChange && canonicalRes.Changed {
		t, err := toml.LoadBytes(canonicalContent)
		if err != nil {
			return nil, err
		}
		if err := cluster.writeTomlAtomically(t, path); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Error writing canonical local template file %s: %s", path, err)
			return nil, err
		}
	}
	return canonicalContent, nil
}

func (cluster *Cluster) getTemplateContent(template string, forceRefresh bool) ([]byte, error) {
	normalizedTemplateName, err := normalizeTemplateIdentifier(template)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error validating template path %s: %s", template, err)
		return nil, err
	}

	if !forceRefresh {
		// Walk layered read paths: cluster-specific first, then global.
		for _, readPath := range cluster.resolveTemplateReadPaths(normalizedTemplateName) {
			content, err := cluster.loadAndValidateLocalTemplate(readPath, normalizedTemplateName, true)
			if err == nil {
				return content, nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}

	// Try repo cache, then fall back to embedded/shared dir
	content, err := cluster.loadTemplateFromRepo(normalizedTemplateName, forceRefresh)
	if err != nil {
		if IsSharedDummyTemplate(normalizedTemplateName) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Error loading template %s from repo cache, trying shared dummy fallback: %s", normalizedTemplateName, err)
			content, err = cluster.loadTemplateFromShared(normalizedTemplateName)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("template %q not found in local or repo cache", normalizedTemplateName)
		}
	}

	canonicalContent, _, err := config.CanonicalizeAppTemplateTOML(content)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Error canonicalizing template file %s: %s", normalizedTemplateName, err)
		return nil, err
	}
	if err := cluster.validateTemplateDeploymentPaths(canonicalContent, normalizedTemplateName); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"Invalid template file %s after canonicalization: %s", normalizedTemplateName, err)
		return nil, err
	}

	return canonicalContent, nil
}

func (cluster *Cluster) validateTemplateDeploymentPaths(content []byte, template string) error {
	appViper := viper.New()
	appViper.SetConfigType("toml")
	if err := appViper.ReadConfig(bytes.NewBuffer(content)); err != nil {
		return err
	}

	var appcnf config.AppConfig
	appcnf.Deployment = config.NewDeploymentConfig()
	if err := appViper.Unmarshal(&appcnf); err != nil {
		return err
	}

	if appcnf.Deployment == nil {
		return nil
	}
	if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
		for _, resolveErr := range resolveErrs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
				"Template %q deployment path resolution error: %v", template, resolveErr)
		}
		return fmt.Errorf("invalid deployment path mapping for template %q", template)
	}

	return nil
}

func (cluster *Cluster) LoadTemplateToViper(content []byte) (*viper.Viper, error) {
	if content == nil {
		return nil, errors.New("template content is empty")
	}

	// read parsed content (toml format) and merge it into the app configuration
	appViper := viper.New()
	appViper.SetConfigType("toml")
	err := appViper.ReadConfig(bytes.NewBuffer(content))
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error reading parsed template file: %s", err)
		return nil, err
	}

	return appViper, nil
}

func (cluster *Cluster) ParseTemplateContent(app *App, content []byte) ([]byte, error) {
	var err error

	if app.AppClusterSubstitute == "" {
		// If the app cluster substitute is empty, generate it
		app.AppClusterSubstitute, err = cluster.GetAppsSubstitutionJSon(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error getting app cluster substitute for %s:%s: %s", app.Host, app.Port, err)
		}
	}

	// If the app cluster substitute is still empty, use the template as is
	var parsed = string(content)
	if app.AppClusterSubstitute != "" {
		parsed, err = cluster.ParseAppTemplate(string(content), app.AppClusterSubstitute)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error parsing template file: %s", err)
			return nil, err
		}
	}
	return []byte(parsed), nil
}

func (cluster *Cluster) RefreshAppTemplateMD5(app *App) error {
	if app.IsHashingTemplate {
		return nil
	}

	app.IsHashingTemplate = true
	defer func() {
		app.IsHashingTemplate = false
	}()

	// Get the current app template MD5
	res, err := cluster.OpenSVCGetAppTemplateV2(app)
	if err != nil {
		return err
	}

	app.TemplateMD5 = misc.GetMD5HashFromBytes(res)

	if app.TemplateMD5Prov != "" {
		if app.HasTemplateMD5Diff() {
			if app.HasProvisionCookie() {
				app.SetReprovCookie()
			}
		} else {
			app.DelReprovisionCookie()
		}
	}
	return nil
}

func (cluster *Cluster) RefreshAllAppTemplateMD5() {
	for _, app := range cluster.Apps {
		if app.IsHashingTemplate {
			continue
		}

		err := cluster.RefreshAppTemplateMD5(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not refresh app template MD5 for app %s:  %s ", app.GetId(), err)
		}
	}
}

func (cluster *Cluster) LoadAppTemplateMD5Provisioned(app *App) error {
	templatefile := filepath.Join(app.Datadir, "opensvc_template.json")
	_, err := os.Stat(templatefile)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(templatefile)
	if err != nil {
		return err
	}

	app.TemplateMD5Prov = misc.GetMD5HashFromBytes(content)
	return nil
}

func (cluster *Cluster) LoadAllAppTemplateMD5Provisioned() {
	for _, app := range cluster.Apps {
		err := cluster.LoadAppTemplateMD5Provisioned(app)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not load app template MD5 provisioned for app %s:  %s ", app.GetId(), err)
		}

		if cluster.RefreshTemplateMD5Chan == nil {
			cluster.RefreshTemplateMD5Chan = make(chan *App, 10)
		}
		cluster.EnqueueRefreshAppTemplateMD5(app)
	}
}

// InitiateRefreshTemplateMD5Worker starts a worker to refresh app template MD5 hashes.
// It exits cleanly when the channel is closed.
func (cluster *Cluster) CreateTemplateMD5Channel() {
	cluster.RefreshTemplateMD5Chan = make(chan *App, 10)
}

func (cluster *Cluster) InitiateRefreshTemplateMD5Worker() {
	for app := range cluster.RefreshTemplateMD5Chan {
		if app == nil {
			continue
		}

		if err := cluster.RefreshAppTemplateMD5(app); err != nil {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModOrchestrator,
				config.LvlErr,
				"Cannot refresh app template MD5 for app %s: %s",
				app.GetId(), err,
			)
		}
	}

	cluster.LogModulePrintf(
		cluster.Conf.Verbose,
		config.ConstLogModOrchestrator,
		config.LvlInfo,
		"RefreshTemplateMD5 worker stopped (channel closed)",
	)
}

func (cluster *Cluster) CloseRefreshTemplateMD5Worker() {
	close(cluster.RefreshTemplateMD5Chan)
}

func (cluster *Cluster) EnqueueRefreshAppTemplateMD5(app *App) {
	if app == nil {
		return
	}

	defer func() {
		_ = recover() // ignore panic if channel is closed
	}()

	select {
	case cluster.RefreshTemplateMD5Chan <- app:
		// Enqueued successfully
	default:
		// Channel full — drop silently
	}
}

func (cluster *Cluster) CheckAppsCredit() {
	for _, app := range cluster.Apps {
		if app != nil {
			app.CheckAppCredits()
		}
	}
}
