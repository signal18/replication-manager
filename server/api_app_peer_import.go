// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// api_app_peer_import.go implements a manual, opt-in, per-app import of app
// monitoring definitions from the arbitration peer (ArbitrationPeerHosts) —
// never automatic, never import-all. See
// doc/implementation/server/APP_MONITOR_PEER_IMPORT_PLAN.md for the design.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// PeerAppInventoryItem describes one app monitor exposed by the peer
// inventory endpoint. Host/Port are the persisted identity
// (app.AppConfig.AppHost/AppPort — what SaveApp() names the file after and
// LoadAppConfig() dedupes on) and are the only fields used as an import
// selection key. RuntimeHost is app.GetHost(), which ProvNetCNI rewrites
// with a cluster-svc suffix for routing; it is included for display/debug
// only and must never be used to select or key an import.
type PeerAppInventoryItem struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	RuntimeHost string `json:"runtimeHost,omitempty"`
	Id          string `json:"id,omitempty"`
	Template    string `json:"template,omitempty"`
	DockerImage string `json:"dockerImage,omitempty"`
}

// PeerAppInventory is the response of the peer inventory endpoint, scoped to one cluster.
type PeerAppInventory struct {
	ClusterName string                 `json:"clusterName"`
	URI         string                 `json:"uri"`
	Apps        []PeerAppInventoryItem `json:"apps"`
}

// AppImportPreviewItem is the per-app status returned by the import preview endpoint.
type AppImportPreviewItem struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Id          string `json:"id,omitempty"`
	Template    string `json:"template,omitempty"`
	DockerImage string `json:"dockerImage,omitempty"`
	// Status is one of: importable, already_exists, unsupported_same_host, invalid_peer.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// AppImportPreviewResult is the response of the import preview endpoint.
type AppImportPreviewResult struct {
	ClusterName string                 `json:"clusterName"`
	PeerURI     string                 `json:"peerUri"`
	Apps        []AppImportPreviewItem `json:"apps"`
}

// appImportSelector identifies one peer app to import, keyed by host+port —
// the same key used by the existing add/drop app monitor flows.
type appImportSelector struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

// appImportApplyRequest is the request body of the import apply endpoint.
// Selection is always explicit: there is no import-all path.
type appImportApplyRequest struct {
	Apps []appImportSelector `json:"apps"`
}

// AppImportApplyItem is the per-app outcome returned by the import apply endpoint.
type AppImportApplyItem struct {
	Host   string `json:"host"`
	Port   string `json:"port"`
	Status string `json:"status"` // imported | rejected
	Reason string `json:"reason,omitempty"`
}

// AppImportApplyResult is the response of the import apply endpoint.
type AppImportApplyResult struct {
	ClusterName string               `json:"clusterName"`
	Apps        []AppImportApplyItem `json:"apps"`
}

// samePeerCluster verifies the arbitration peer is monitoring the exact same
// cluster as this node: same cluster name AND same canonical instance URI
// (server/api_register.go registeredInstanceURI). Fails closed — an empty
// local or peer URI is never treated as a match.
func (repman *ReplicationManager) samePeerCluster(localClusterName string, peerInv PeerAppInventory) error {
	if peerInv.ClusterName != localClusterName {
		return fmt.Errorf("peer cluster name %q does not match local cluster name %q", peerInv.ClusterName, localClusterName)
	}
	localURI := repman.registeredInstanceURI()
	if localURI == "" || peerInv.URI == "" || localURI != peerInv.URI {
		return fmt.Errorf("peer instance URI %q does not match local instance URI %q", peerInv.URI, localURI)
	}
	return nil
}

// peerAppInventory fetches the app inventory for clusterName from the
// arbitration peer using an already-established login session.
func (repman *ReplicationManager) peerAppInventory(base, token, clusterName string) (PeerAppInventory, error) {
	var inv PeerAppInventory
	req, err := http.NewRequest("GET", base+"/api/clusters/"+clusterName+"/apps/peer-import/inventory", nil)
	if err != nil {
		return inv, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return inv, fmt.Errorf("peer inventory request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return inv, fmt.Errorf("peer inventory rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &inv); err != nil {
		return inv, fmt.Errorf("peer inventory parse failed: %w", err)
	}
	return inv, nil
}

// peerAppList logs into the arbitration peer (ArbitrationPeerHosts) with the
// shared cluster-test credentials and fetches its app inventory for clusterName.
func (repman *ReplicationManager) peerAppList(clusterName string) (PeerAppInventory, error) {
	base, token, err := repman.peerSplitBrainLogin(clusterName)
	if err != nil {
		return PeerAppInventory{}, err
	}
	return repman.peerAppInventory(base, token, clusterName)
}

// peerAppExport fetches the peer's TOML export for one app (host+port) using
// an already-established login session.
func (repman *ReplicationManager) peerAppExport(base, token, clusterName, host, port string) (string, error) {
	u := fmt.Sprintf("%s/api/clusters/%s/apps/peer-import/export?host=%s&port=%s",
		base, clusterName, url.QueryEscape(host), url.QueryEscape(port))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("peer app export request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peer app export rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Toml string `json:"toml"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Toml == "" {
		return "", fmt.Errorf("peer app export parse failed: %s", strings.TrimSpace(string(body)))
	}
	return payload.Toml, nil
}

// appImportPreview logs into the arbitration peer, fetches its app inventory,
// verifies same-cluster identity, and classifies each peer app against the
// local app list by host+port. Read-only: never writes local state.
func (repman *ReplicationManager) appImportPreview(mycluster *cluster.Cluster) (AppImportPreviewResult, error) {
	result := AppImportPreviewResult{ClusterName: mycluster.Name}

	inv, err := repman.peerAppList(mycluster.Name)
	if err != nil {
		return result, err
	}
	result.PeerURI = inv.URI

	if err := repman.samePeerCluster(mycluster.Name, inv); err != nil {
		// Fail closed: every candidate is invalid_peer, no partial success.
		for _, a := range inv.Apps {
			result.Apps = append(result.Apps, AppImportPreviewItem{
				Host: a.Host, Port: a.Port, Id: a.Id, Template: a.Template, DockerImage: a.DockerImage,
				Status: "invalid_peer", Reason: err.Error(),
			})
		}
		return result, nil
	}

	// Same-host/different-port on the peer's own inventory is unsupported by
	// the current host-based storage layout — flag every app on an ambiguous
	// host so the operator sees it before selecting anything.
	peerPortsByHost := make(map[string]map[string]bool)
	for _, a := range inv.Apps {
		if peerPortsByHost[a.Host] == nil {
			peerPortsByHost[a.Host] = make(map[string]bool)
		}
		peerPortsByHost[a.Host][a.Port] = true
	}

	for _, a := range inv.Apps {
		item := AppImportPreviewItem{Host: a.Host, Port: a.Port, Id: a.Id, Template: a.Template, DockerImage: a.DockerImage}
		switch {
		case len(peerPortsByHost[a.Host]) > 1:
			item.Status = "unsupported_same_host"
			item.Reason = "peer has multiple ports on the same host; current storage layout is host-based"
		case mycluster.HasAppHostPort(a.Host, a.Port):
			item.Status = "already_exists"
		case mycluster.HasAppHost(a.Host):
			item.Status = "unsupported_same_host"
			item.Reason = "local cluster already has an app on this host with a different port"
		default:
			item.Status = "importable"
		}
		result.Apps = append(result.Apps, item)
	}

	return result, nil
}

// appImportApply imports the explicitly selected peer apps (host+port) into
// mycluster. It re-verifies the same-cluster gate and the host-collision
// guards independently of any prior preview call, and processes each
// selection independently: a rejected app never blocks the others, and a
// rejected app never partially writes local state (cluster.ImportAppConfig
// only mutates on full success).
func (repman *ReplicationManager) appImportApply(mycluster *cluster.Cluster, selected []appImportSelector) (AppImportApplyResult, error) {
	result := AppImportApplyResult{ClusterName: mycluster.Name}
	if len(selected) == 0 {
		return result, errors.New("no apps selected for import")
	}

	base, token, err := repman.peerSplitBrainLogin(mycluster.Name)
	if err != nil {
		return result, err
	}

	inv, err := repman.peerAppInventory(base, token, mycluster.Name)
	if err != nil {
		return result, err
	}
	if err := repman.samePeerCluster(mycluster.Name, inv); err != nil {
		return result, err
	}

	peerByHostPort := make(map[string]bool, len(inv.Apps))
	peerPortsByHost := make(map[string]map[string]bool, len(inv.Apps))
	for _, a := range inv.Apps {
		peerByHostPort[a.Host+"|"+a.Port] = true
		if peerPortsByHost[a.Host] == nil {
			peerPortsByHost[a.Host] = make(map[string]bool)
		}
		peerPortsByHost[a.Host][a.Port] = true
	}

	// Guard: selections that put different ports on the same host cannot be
	// mapped safely onto the current host-based storage layout. Reject the
	// whole ambiguous group up front, before any peer export or local write.
	selectedPortsByHost := make(map[string]map[string]bool)
	for _, s := range selected {
		if selectedPortsByHost[s.Host] == nil {
			selectedPortsByHost[s.Host] = make(map[string]bool)
		}
		selectedPortsByHost[s.Host][s.Port] = true
	}

	for _, s := range selected {
		item := AppImportApplyItem{Host: s.Host, Port: s.Port}

		if len(selectedPortsByHost[s.Host]) > 1 {
			item.Status = "rejected"
			item.Reason = "same host with different ports selected together is not supported"
			result.Apps = append(result.Apps, item)
			continue
		}
		// Mirror preview's own-peer-ambiguity check: reject even a single
		// selection if the peer itself reports more than one port on this
		// host, so apply never accepts something preview would have flagged
		// unsupported_same_host, whether or not preview was actually called.
		if len(peerPortsByHost[s.Host]) > 1 {
			item.Status = "rejected"
			item.Reason = "peer has multiple ports on this host; current storage layout is host-based"
			result.Apps = append(result.Apps, item)
			continue
		}
		if !peerByHostPort[s.Host+"|"+s.Port] {
			item.Status = "rejected"
			item.Reason = "not present in peer inventory"
			result.Apps = append(result.Apps, item)
			continue
		}

		tomlContent, err := repman.peerAppExport(base, token, mycluster.Name, s.Host, s.Port)
		if err != nil {
			item.Status = "rejected"
			item.Reason = "peer export failed: " + err.Error()
			result.Apps = append(result.Apps, item)
			continue
		}

		if err := mycluster.ImportAppConfig(s.Host, s.Port, tomlContent); err != nil {
			item.Status = "rejected"
			item.Reason = err.Error()
			result.Apps = append(result.Apps, item)
			continue
		}

		item.Status = "imported"
		result.Apps = append(result.Apps, item)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handlerMuxAppPeerImportInventory is the peer-side endpoint: it lists this
// node's own apps for clusterName so that another repman node monitoring the
// same cluster can preview an import. Read-only, scoped to one cluster.
func (repman *ReplicationManager) handlerMuxAppPeerImportInventory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	inv := buildPeerAppInventory(mycluster)
	inv.URI = repman.registeredInstanceURI()

	json.NewEncoder(w).Encode(inv)
}

// buildPeerAppInventory builds the peer inventory response for mycluster.
// Selection keys are the persisted identity (app.AppConfig.AppHost/AppPort)
// — see PeerAppInventoryItem. Apps with no AppConfig, or an empty persisted
// host/port, are omitted (fail closed: an app that cannot be identified by
// its persisted key must never be offered for import).
func buildPeerAppInventory(mycluster *cluster.Cluster) PeerAppInventory {
	inv := PeerAppInventory{ClusterName: mycluster.Name}
	for _, app := range mycluster.GetAppsCopy() {
		if app == nil || app.AppConfig == nil || app.AppConfig.AppHost == "" || app.AppConfig.AppPort == "" {
			continue
		}
		inv.Apps = append(inv.Apps, PeerAppInventoryItem{
			Host:        app.AppConfig.AppHost,
			Port:        app.AppConfig.AppPort,
			RuntimeHost: app.GetHost(),
			Id:          app.Id,
			Template:    app.AppConfig.ProvAppTemplate,
			DockerImage: app.AppConfig.ProvAppDockerImg,
		})
	}
	return inv
}

// handlerMuxAppPeerImportExport is the peer-side endpoint: it exports one
// app's config, identified by its persisted host+port
// (app.AppConfig.AppHost/AppPort), using the same TOML shape already
// persisted locally by SaveApp()/SaveAppConfigFile().
func (repman *ReplicationManager) handlerMuxAppPeerImportExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	host := r.URL.Query().Get("host")
	port := r.URL.Query().Get("port")
	if host == "" || port == "" {
		http.Error(w, "host and port query parameters are required", http.StatusBadRequest)
		return
	}

	// Look up by persisted identity (AppConfig.AppHost/AppPort), not runtime
	// host: GetAppByHostPort compares app.GetHost(), which ProvNetCNI
	// rewrites — see PeerAppInventoryItem.
	app := mycluster.GetAppByConfig(&config.AppConfig{AppHost: host, AppPort: port})
	if app == nil || app.AppConfig == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	data, err := toml.Marshal(app.AppConfig)
	if err != nil {
		http.Error(w, "Error marshalling app config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(struct {
		Host string `json:"host"`
		Port string `json:"port"`
		Toml string `json:"toml"`
	}{Host: host, Port: port, Toml: string(data)})
}

// handlerMuxAppPeerImportPreview is the local-side, read-only preview step:
// it fetches the peer's inventory and classifies every peer app without
// writing any local state.
func (repman *ReplicationManager) handlerMuxAppPeerImportPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	result, err := repman.appImportPreview(mycluster)
	if err != nil {
		http.Error(w, "Error building import preview: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

// handlerMuxAppPeerImportApply is the local-side apply step: it imports only
// the explicitly selected apps (host+port). There is no import-all path.
func (repman *ReplicationManager) handlerMuxAppPeerImportApply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var body appImportApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Apps) == 0 {
		http.Error(w, "No apps selected for import", http.StatusBadRequest)
		return
	}

	result, err := repman.appImportApply(mycluster, body.Apps)
	if err != nil {
		http.Error(w, "Error applying import: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}
