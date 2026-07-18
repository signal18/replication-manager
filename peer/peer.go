package peer

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/misc"
)

type PeerCluster struct {
	ClusterName                            string    `json:"cluster-name"`
	ApiPublicUrl                           string    `json:"api-public-url"`
	ApiCredentialsAclAllow                 string    `json:"api-credentials-acl-allow"`
	ApiCredentialsAclAllowExternal         string    `json:"api-credentials-acl-allow-external"`
	ProvDbMemory                           int       `json:"prov-db-memory,string"`
	ProvDbCpuCores                         int       `json:"prov-db-cpu-cores,string"`
	ProvDbDiskIops                         int64     `json:"prov-db-disk-iops,string"`
	ProvDbDiskSize                         int64     `json:"prov-db-disk-size,string"`
	ProvServicePlan                        string    `json:"prov-service-plan"`
	ProvOrchestrator                       string    `json:"prov-orchestrator"`
	Cloud18Domain                          string    `json:"cloud18-domain"`
	Cloud18PlatformDescription             string    `json:"cloud18-platform-description"`
	Cloud18Shared                          bool      `json:"cloud18-shared,string"`
	Cloud18Peer                            bool      `json:"cloud18-peer,string"`
	Cloud18SubDomain                       string    `json:"cloud18-sub-domain"`
	Cloud18SubDomainZone                   string    `json:"cloud18-sub-domain-zone"`
	Cloud18MonthlyInfraCost                float64   `json:"cloud18-monthly-infra-cost,string"`
	Cloud18MonthlyLicenseCost              float64   `json:"cloud18-monthly-license-cost,string"`
	Cloud18MonthlySysopsCost               float64   `json:"cloud18-monthly-sysops-cost,string"`
	Cloud18MonthlyDbopsCost                float64   `json:"cloud18-monthly-dbops-cost,string"`
	Cloud18CostCurrency                    string    `json:"cloud18-cost-currency"`
	Cloud18InfraCPUFreq                    string    `json:"cloud18-infra-cpu-freq"`
	Cloud18InfraCPUModel                   string    `json:"cloud18-infra-cpu-model"`
	Cloud18InfraGeoLocalizations           string    `json:"cloud18-infra-geo-localizations"`
	Cloud18InfraPublicBandwidth            float64   `json:"cloud18-infra-public-bandwidth,string"`
	Cloud18InfraDataCenters                string    `json:"cloud18-infra-data-centers"`
	Cloud18OpenDbops                       bool      `json:"cloud18-open-dbops,string"`
	Cloud18SubscribedDbops                 bool      `json:"cloud18-subscribed-dbops,string"`
	Cloud18OpenSysops                      bool      `json:"cloud18-open-sysops,string"`
	Cloud18DatabaseReadWriteSplitSrvRecord string    `json:"cloud18-database-read-write-split-srv-record"`
	Cloud18DatabaseReadSrvRecord           string    `json:"cloud18-database-read-srv-record"`
	Cloud18DatabaseReadWriteSrvRecord      string    `json:"cloud18-database-read-write-srv-record"`
	Cloud18SlaResponseTime                 float64   `json:"cloud18-sla-response-time,string"`
	Cloud18SlaRepairTime                   float64   `json:"cloud18-sla-repair-time,string"`
	Cloud18SlaProvisionTime                float64   `json:"cloud18-sla-provision-time,string"`
	Cloud18PromotionPct                    float64   `json:"cloud18-promotion-pct,string"`
	Cloud18ExtDbOps                        string    `json:"cloud18-external-dbops"`
	Cloud18ExtSysOps                       string    `json:"cloud18-external-sysops"`
	Cloud18InfraCertifications             string    `json:"cloud18-infra-certifications"`
	RepmgrVersion                          string    `json:"repmgrVersion"`
	IsDown                                 bool      `json:"isDown"`
	IsMasterDown                           bool      `json:"isMasterDown"`
	IsFailable                             bool      `json:"isFailable"`
	IsProvisioned                          bool      `json:"isProvisioned"`
	LastUpdate                             time.Time `json:"lastUpdate"`
}

type PeerHealth struct {
	IsDown        bool      `json:"isDown"`
	IsMasterDown  bool      `json:"isMasterDown"`
	IsFailable    bool      `json:"isFailable"`
	IsProvisioned bool      `json:"isProvisioned"`
	LastUpdate    time.Time `json:"lastUpdate"`
}

type PeerNodeStatus struct {
	Error      string
	LastUpdate time.Time
}

// PeerManager manages peer clusters.
type PeerManager struct {
	mu                sync.RWMutex
	PeerUser          string
	PeerPassword      string
	ApiURL            string
	PeerURL           map[string]*PeerNodeStatus
	PeerClusters      map[string]*PeerCluster
	PeerForSale       map[string]*PeerCluster
	PeerUserClusters  map[string]map[string]*PeerCluster
	UserClusterAccess map[string]map[string]struct{} // Optimized mapping for user access to clusters
	Clients           map[string]*PeerClient
	Interval          int
	MissingSince      time.Time
	HealthMode        string // "peering" (HTTP poll) or "pulling" (BO via peer.json)
}

// NewPeerManager initializes a new PeerManager.
func NewPeerManager(interval int) *PeerManager {
	return &PeerManager{
		PeerClusters:      make(map[string]*PeerCluster),
		PeerForSale:       make(map[string]*PeerCluster),
		PeerUserClusters:  make(map[string]map[string]*PeerCluster),
		UserClusterAccess: make(map[string]map[string]struct{}),
		PeerURL:           make(map[string]*PeerNodeStatus),
		Clients:           make(map[string]*PeerClient),
		Interval:          interval,
	}
}

func (pm *PeerManager) SetInterval(interval int) {
	pm.Interval = interval
}

// SetPeerUser sets the username for peer communication.
func (pm *PeerManager) SetPeerCredentials(username, password string) {
	pm.PeerUser = username
	pm.PeerPassword = password
}

func (pm *PeerManager) SetApiPublicURL(apiURL string) {
	pm.ApiURL = apiURL
}

// SetHealthMode safely updates HealthMode. It can be changed at runtime from HTTP
// handlers (registration, global settings) while BatchUpdateClusters reads it under
// pm.mu, so the write must take the same lock to avoid a data race.
func (pm *PeerManager) SetHealthMode(mode string) {
	pm.mu.Lock()
	pm.HealthMode = mode
	pm.mu.Unlock()
}

func (pm *PeerManager) NewClient(baseURL string) *PeerClient {
	pclient := NewPeerClient(baseURL, 10*time.Second)
	pm.Clients[baseURL] = pclient
	return pclient
}

// HasPeerURL checks if an origin exists in the PeerURL map.
func (pm *PeerManager) HasPeerURL(origin string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	_, exists := pm.PeerURL[origin]
	return exists
}

// BatchUpdateClusters updates multiple clusters at once.
func (pm *PeerManager) BatchUpdateClusters(clusterUpdates []*PeerCluster, removeOld bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	updatedNames := make(map[string]bool)

	for _, pc := range clusterUpdates {
		hashID := GetPeerHashID(pc)

		if cl, exists := pm.PeerClusters[hashID]; exists {
			if pm.HealthMode == "pulling" && pc.RepmgrVersion != "" {
				// Pulling mode with known version: use health from peer.json (BO).
				pc.LastUpdate = time.Now()
			} else {
				// Peering mode or unknown version: preserve health from HTTP polling.
				pc.IsDown = cl.IsDown
				pc.IsMasterDown = cl.IsMasterDown
				pc.IsFailable = cl.IsFailable
				pc.IsProvisioned = cl.IsProvisioned
				pc.LastUpdate = cl.LastUpdate
			}
			*cl = *pc
		} else {
			if pm.HealthMode == "pulling" && pc.RepmgrVersion != "" {
				pc.LastUpdate = time.Now()
			}
			pm.PeerClusters[hashID] = pc
		}

		pm.ReloadUsers(pc)
		updatedNames[hashID] = true

		if _, exists := pm.PeerURL[pc.ApiPublicUrl]; !exists {
			pm.PeerURL[pc.ApiPublicUrl] = new(PeerNodeStatus)
		}
	}

	if removeOld {
		for hashID := range pm.PeerClusters {
			if !updatedNames[hashID] {
				pm.removeCluster(hashID)
			}
		}
	}

	// NOTE: health polling is intentionally NOT started here. This runs inside
	// the peer package with no session-user context, so it can only poll the
	// flat, unscoped PeerURL list — which includes for-sale clusters this
	// instance has no relationship to (O(N^2) fan-out, dark-peer connection
	// leak). The caller (server_git.go, after this returns) invokes the
	// server-driven, session-scoped dispatchPeerHealthPoll instead, so live
	// polling is always restricted to PeerUserClusters for connected users.
	// See doc/implementation/peer/MARKETPLACE.md §6.
}

// removeCluster (internal function, assumes lock is held).
func (pm *PeerManager) removeCluster(hashID string) {
	delete(pm.PeerClusters, hashID)
	delete(pm.PeerForSale, hashID)
	pm.removeClusterFromUsers(hashID)
}

// DropAllClusters removes all clusters from the PeerManager.
func (pm *PeerManager) DropAllClusters() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.MissingSince.IsZero() {
		// Mark the time when the file is missing.
		pm.MissingSince = time.Now()
		return
	}

	// If the missing time is more than a minute, clear the maps.
	if time.Since(pm.MissingSince) < time.Minute {
		return
	}

	// Clear all maps associated with clusters.
	pm.PeerClusters = make(map[string]*PeerCluster)
	pm.PeerForSale = make(map[string]*PeerCluster)
	pm.PeerUserClusters = make(map[string]map[string]*PeerCluster)
	pm.UserClusterAccess = make(map[string]map[string]struct{})
	pm.MissingSince = time.Time{}
}

// GetCluster retrieves a pc.
func (pm *PeerManager) GetCluster(hashID string) (*PeerCluster, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cluster, exists := pm.PeerClusters[hashID]
	return cluster, exists
}

// normPublicURL normalizes an api-public-url for identity comparison: lowercased,
// scheme-stripped, no trailing slash. So "https://Host/" and "host" compare equal.
func normPublicURL(u string) string {
	u = strings.TrimSpace(strings.ToLower(u))
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimRight(u, "/")
}

// isOwnCluster reports whether a peer cluster is hosted by THIS repman instance
// (its api-public-url matches ours). Own clusters already appear in the LOCAL
// cluster list, so they must be excluded from the peer / for-sale views — otherwise
// they show up twice. Assumes the caller holds pm.mu (read or write); pm.ApiURL is
// set once at startup and never mutated afterwards.
func (pm *PeerManager) isOwnCluster(pc *PeerCluster) bool {
	return pc != nil && pm.ApiURL != "" && normPublicURL(pc.ApiPublicUrl) == normPublicURL(pm.ApiURL)
}

// GetUserClusters retrieves all clusters a user has access to, excluding clusters
// this instance hosts itself (they are already in the local cluster list).
func (pm *PeerManager) GetUserClusters(username string) []*PeerCluster {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	clusters := []*PeerCluster{}
	if pcs, ok := pm.PeerUserClusters[username]; ok {
		for _, pc := range pcs {
			if pm.isOwnCluster(pc) {
				continue
			}
			clusters = append(clusters, pc)
		}
	}

	slices.SortStableFunc(clusters, SortPeerFunc)
	return clusters
}

func (pm *PeerManager) GetPeerNodeStatus() map[string]*PeerNodeStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.PeerURL
}

// GetPeerNodesJSON returns the peer node statuses as JSON. It marshals a value-copy
// snapshot taken under the lock — never the live *PeerNodeStatus pointers — because
// pollPeerHealth mutates Error/LastUpdate concurrently off the status-API path.
func (pm *PeerManager) GetPeerNodesJSON() ([]byte, error) {
	pm.mu.RLock()
	snapshot := make(map[string]PeerNodeStatus, len(pm.PeerURL))
	for url, ns := range pm.PeerURL {
		if ns != nil {
			snapshot[url] = *ns
		}
	}
	pm.mu.RUnlock()
	return json.MarshalIndent(snapshot, "", "\t")
}

// GetUserClustersJSON returns a JSON string of all clusters assigned to a user.
func (pm *PeerManager) GetUserClustersJSON(username string) ([]byte, error) {
	clusters := pm.GetUserClusters(username)
	return json.MarshalIndent(clusters, "", "\t")
}

// GetSaleClustersJSON returns a JSON string of all clusters available for sale.
func (pm *PeerManager) GetSaleClustersJSON() ([]byte, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	clusters := make([]*PeerCluster, 0, len(pm.PeerForSale))
	for _, pc := range pm.PeerForSale {
		if pm.isOwnCluster(pc) {
			continue
		}
		clusters = append(clusters, pc)
	}

	slices.SortStableFunc(clusters, SortPeerFunc)

	return json.MarshalIndent(clusters, "", "\t")
}

// removeClusterFromUsers removes a cluster from user mapping.
func (pm *PeerManager) removeClusterFromUsers(hashID string) {
	for _, clusters := range pm.PeerUserClusters {
		delete(clusters, hashID)
	}
}

func (pm *PeerManager) ReloadUsers(pc *PeerCluster) {
	hashID := GetPeerHashID(pc)
	userlist := make(map[string]struct{})
	var forSale bool = pc.Cloud18Shared
	for _, acl := range strings.Split(pc.ApiCredentialsAclAllow+","+pc.ApiCredentialsAclAllowExternal, ",") {
		uname, _, _, roles := misc.SplitAcls(acl)
		if _, ok := pm.UserClusterAccess[uname]; !ok {
			pm.UserClusterAccess[uname] = make(map[string]struct{})
		}
		if _, ok := pm.PeerUserClusters[uname]; !ok {
			pm.PeerUserClusters[uname] = make(map[string]*PeerCluster)
		}

		pm.UserClusterAccess[uname][hashID] = struct{}{}
		pm.PeerUserClusters[uname][hashID] = pc
		userlist[uname] = struct{}{}
		if forSale {
			isSponsor := strings.Contains(roles, "sponsor")
			if isSponsor {
				forSale = false
				continue
			}

			isPending := strings.Contains(roles, "pending")
			if isPending {
				forSale = false
				continue
			}
		}
	}

	for uname, _ := range pm.UserClusterAccess {
		if _, ok := userlist[uname]; !ok {
			delete(pm.UserClusterAccess[uname], hashID)
			delete(pm.PeerUserClusters[uname], hashID)
		}
	}

	if forSale {
		pm.PeerForSale[hashID] = pc
	} else {
		delete(pm.PeerForSale, hashID)
	}
}

func (pm *PeerManager) GetHealthStatus(pclient *PeerClient) error {
	hstatus, hbody, err := pclient.Get("/api/health")
	if err != nil {
		return err
	}

	update := time.Now()

	if hstatus != http.StatusOK {
		return fmt.Errorf("health check failed with status %d: %s", hstatus, string(hbody))
	} else {
		healths := make(map[string]PeerHealth)
		if err := json.Unmarshal(hbody, &healths); err != nil {
			return fmt.Errorf("failed to parse health status: %s", err)
		}

		// Lock the map mutation: this runs off-lock (called from pollPeerHealth
		// after the network I/O) and BatchUpdateClusters writes PeerClusters under
		// the same lock.
		pm.mu.Lock()
		for clustername, status := range healths {
			hashID := GetHashID(pclient.baseURL, clustername)
			if pc, exists := pm.PeerClusters[hashID]; exists {
				pc.IsDown = status.IsDown
				pc.IsMasterDown = status.IsMasterDown
				pc.IsFailable = status.IsFailable
				pc.IsProvisioned = status.IsProvisioned
				pc.LastUpdate = update
			}
		}
		pm.mu.Unlock()
	}

	return nil
}

func (pm *PeerManager) UpdateHealthStatus(healths map[string]PeerHealth) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	update := time.Now()
	for clustername, status := range healths {
		hashID := GetHashID(pm.ApiURL, clustername)
		if pc, exists := pm.PeerClusters[hashID]; exists {
			pc.IsDown = status.IsDown
			pc.IsMasterDown = status.IsMasterDown
			pc.IsFailable = status.IsFailable
			pc.IsProvisioned = status.IsProvisioned
			pc.LastUpdate = update
		}
	}
}

// relevantPeerURLs returns the set of peer API URLs this instance may live-poll:
// the registering user's peers (own fleet — always) plus each active-session user's
// peers. These come only from PeerUserClusters (own fleet + delegated + sale
// workflows via ReloadUsers' pending/sponsor demotion), so a for-sale catalog
// cluster nobody here is related to — keyed under the seller's user, in PeerForSale —
// never appears. A fresh/browsing instance returns an empty set. Own ApiURL is
// excluded. See doc/implementation/peer/MARKETPLACE.md §6.
func (pm *PeerManager) relevantPeerURLs(registeredUser string, activeUsers []string) map[string]bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	urls := make(map[string]bool)
	addUserPeers := func(user string) {
		clusters, ok := pm.PeerUserClusters[user]
		if !ok {
			return
		}
		for _, pc := range clusters {
			if pc.ApiPublicUrl != "" && pc.ApiPublicUrl != pm.ApiURL {
				urls[pc.ApiPublicUrl] = true
			}
		}
	}

	if registeredUser != "" {
		addUserPeers(registeredUser)
	}
	for _, user := range activeUsers {
		if user != registeredUser {
			addUserPeers(user)
		}
	}
	return urls
}

// GetHealthStatusForActiveUsers polls only the peer URLs that the registered
// user or active session users can access. The registeredUser (cloud18-gitlab-user)
// is always included — own fleet peers are always polled. Other users' peers
// are only polled when they have an active session (non-empty GitToken).
func (pm *PeerManager) GetHealthStatusForActiveUsers(registeredUser string, activeUsers []string) {
	for url := range pm.relevantPeerURLs(registeredUser, activeUsers) {
		pm.pollPeerHealth(url)
	}
}

// pollPeerHealth checks one peer's /api/health, but only if it has not been polled
// within Interval. It "claims" the peer atomically under the lock — FIRST IN WINS:
// the first dispatch to reach a peer advances LastUpdate and does the call, while
// any overlapping dispatch sees a fresh timestamp and skips it. So each peer is
// called at most once per Interval regardless of how many dispatches overlap, and a
// failed attempt is rate-limited exactly like a success (LastUpdate is advanced
// up-front, before the call). All shared-map access (PeerURL / Clients) happens
// under the lock; the network I/O runs AFTER the lock is released, so a slow peer
// never blocks other peers or the BatchUpdateClusters mutator, and there is no
// concurrent-map race. This makes whole-poll serialization (single-flight)
// unnecessary: different peers can still be polled in parallel — only the SAME peer
// is deduplicated. See doc/implementation/peer/MARKETPLACE.md §6.
func (pm *PeerManager) pollPeerHealth(url string) {
	pm.mu.Lock()
	nodestat, ok := pm.PeerURL[url]
	if !ok || url == pm.ApiURL || time.Since(nodestat.LastUpdate) <= time.Duration(pm.Interval)*time.Second {
		pm.mu.Unlock()
		return
	}
	if !misc.IsValidPublicURL(url) {
		nodestat.Error = "not a valid public URL"
		pm.mu.Unlock()
		return
	}
	nodestat.LastUpdate = time.Now() // claim: no other dispatch polls this peer this Interval
	pclient, ok := pm.Clients[url]
	if !ok {
		pclient = pm.NewClient(url)
	}
	pm.mu.Unlock()

	// Network I/O outside the lock. This goroutine owns the claim, but nodestat is
	// also read by GetPeerNodesJSON (status API), so its fields are written under the
	// lock via setNodeError, not raw.
	if token, ok := pclient.headers["Authorization"]; !ok || token == "" {
		// Best-effort login: /api/health is PUBLIC (curl of any dbaas-*.signal18.io
		// /api/health returns 200 with no auth). A login failure — the SSO user not
		// being provisioned on the peer, a wrong password, etc. — must NOT block the
		// health/connectivity check, which needs no token. Login only matters for
		// richer authenticated calls (e.g. /api/clusters).
		_ = pclient.PeerLogin(pm.PeerUser, pm.PeerPassword)
	}
	if err := pm.GetHealthStatus(pclient); err != nil {
		pm.setNodeError(nodestat, fmt.Sprintf("failed to get health status: %s", err))
		return
	}
	pm.setNodeError(nodestat, "")
}

// setNodeError writes ns.Error under the lock. PeerNodeStatus pointers are read by
// the status API (GetPeerNodesJSON), so field writes must be synchronized.
func (pm *PeerManager) setNodeError(ns *PeerNodeStatus, errMsg string) {
	pm.mu.Lock()
	ns.Error = errMsg
	pm.mu.Unlock()
}

func (pm *PeerManager) GetAllHealthStatus() {
	// Snapshot the URL set under the lock, then poll each through the atomic-claim
	// helper (network I/O off-lock). Never iterate pm.PeerURL directly during the
	// calls — BatchUpdateClusters mutates it under the same lock.
	pm.mu.RLock()
	urls := make([]string, 0, len(pm.PeerURL))
	for url := range pm.PeerURL {
		if url != pm.ApiURL {
			urls = append(urls, url)
		}
	}
	pm.mu.RUnlock()

	for _, url := range urls {
		pm.pollPeerHealth(url)
	}
}

// GetHealthStatusForUnknownVersions was removed: it polled every peer with an
// empty RepmgrVersion across the whole catalog (for-sale clusters included),
// with no ownership/relationship scope — an unscoped fan-out that broke the
// marketplace invariant. All live polling now goes through
// GetHealthStatusForActiveUsers (scoped to PeerUserClusters for connected
// users). Version back-fill for peers you have a relationship to happens there;
// for-sale catalog versions come from peer.json (BO), not direct polling.
// See doc/implementation/peer/MARKETPLACE.md §6.

func GetPeerHashID(pc *PeerCluster) string {
	md5Hash := md5.New()
	md5Hash.Write([]byte(pc.ApiPublicUrl + "/" + pc.ClusterName))
	return hex.EncodeToString(md5Hash.Sum(nil))
}

func GetHashID(peerURL, name string) string {
	md5Hash := md5.New()
	md5Hash.Write([]byte(peerURL + "/" + name))
	return hex.EncodeToString(md5Hash.Sum(nil))
}

func SortPeerFunc(a, b *PeerCluster) int {
	if a.Cloud18Domain < b.Cloud18Domain {
		return -1
	} else if a.Cloud18Domain > b.Cloud18Domain {
		return 1
	}

	if a.Cloud18SubDomain < b.Cloud18SubDomain {
		return -1
	} else if a.Cloud18SubDomain > b.Cloud18SubDomain {
		return 1
	}

	if a.Cloud18SubDomainZone < b.Cloud18SubDomainZone {
		return -1
	} else if a.Cloud18SubDomainZone > b.Cloud18SubDomainZone {
		return 1
	}

	if a.ClusterName < b.ClusterName {
		return -1
	} else if a.ClusterName > b.ClusterName {
		return 1
	}

	return 0
}
