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
	Error      error
	LastUpdate time.Time
}

// PeerManager manages peer clusters.
type PeerManager struct {
	mu                sync.RWMutex
	PeerUser          string
	PeerPassword      string
	PeerURL           map[string]*PeerNodeStatus
	PeerClusters      map[string]*PeerCluster
	PeerForSale       map[string]*PeerCluster
	PeerUserClusters  map[string]map[string]*PeerCluster
	UserClusterAccess map[string]map[string]struct{} // Optimized mapping for user access to clusters
	Clients           map[string]*PeerClient
	MissingSince      time.Time
}

// NewPeerManager initializes a new PeerManager.
func NewPeerManager() *PeerManager {
	return &PeerManager{
		PeerClusters:      make(map[string]*PeerCluster),
		PeerForSale:       make(map[string]*PeerCluster),
		PeerUserClusters:  make(map[string]map[string]*PeerCluster),
		UserClusterAccess: make(map[string]map[string]struct{}),
		PeerURL:           make(map[string]*PeerNodeStatus),
		Clients:           make(map[string]*PeerClient),
	}
}

// SetPeerUser sets the username for peer communication.
func (pm *PeerManager) SetPeerCredentials(username, password string) {
	pm.PeerUser = username
	pm.PeerPassword = password
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
			*cl = *pc
		} else {
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

	// Update the health status of all clusters.
	if len(pm.PeerURL) > 0 {
		pm.GetAllHealthStatus()
	}
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

// GetUserClusters retrieves all clusters a user has access to.
func (pm *PeerManager) GetUserClusters(username string) []*PeerCluster {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	clusters := []*PeerCluster{}
	if pcs, ok := pm.PeerUserClusters[username]; ok {
		for _, pc := range pcs {
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

// GetUserClustersJSON returns a JSON string of all clusters assigned to a user.
func (pm *PeerManager) GetPeerNodesJSON() ([]byte, error) {
	nodes := pm.GetPeerNodeStatus()
	return json.MarshalIndent(nodes, "", "\t")
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
	for _, acl := range strings.Split(pc.ApiCredentialsAclAllow, ",") {
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
	}

	return nil
}

func (pm *PeerManager) GetAllHealthStatus() {
	for url, nodestat := range pm.PeerURL {
		// Skip if the URL is not valid.
		if !misc.IsValidPublicURL(url) {
			nodestat.Error = fmt.Errorf("not a valid public URL")
			continue
		}

		pclient, ok := pm.Clients[url]
		if !ok {
			pclient = pm.NewClient(url)
			pm.Clients[url] = pclient
		}

		// Login if no token is set in the client.
		if token, ok := pclient.headers["Authorization"]; !ok || token == "" {
			if err := pclient.PeerLogin(pm.PeerUser, pm.PeerPassword); err != nil {
				nodestat.Error = fmt.Errorf("failed to login: %s", err)
				continue
			}
		}

		if time.Since(nodestat.LastUpdate) > time.Minute {
			if err := pm.GetHealthStatus(pclient); err != nil {
				nodestat.Error = fmt.Errorf("failed to get health status: %s", err)
				continue
			}
			nodestat.LastUpdate = time.Now()
			nodestat.Error = nil
		}
	}
}

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
	if a.ApiPublicUrl < b.ApiPublicUrl {
		return -1
	} else if a.ApiPublicUrl > b.ApiPublicUrl {
		return 1
	}

	if a.ClusterName < b.ClusterName {
		return -1
	} else if a.ClusterName > b.ClusterName {
		return 1
	}

	return 0
}
