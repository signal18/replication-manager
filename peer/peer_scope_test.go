package peer

import (
	"strings"
	"testing"
	"time"
)

// The marketplace invariant (MARKETPLACE.md §6): live polling is scoped to
// PeerUserClusters for the registering user + active-session users. A for-sale
// catalog cluster nobody here is related to must never be in the pollable set;
// a fresh/browsing instance must poll zero sellers; a cluster enters the set only
// via a real relationship (delegation, or a pending/sponsor sale workflow).

func TestRelevantPeerURLsExcludesForSaleIncludesDelegated(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// Delegated to me → in my user set → pollable.
	delegated := &PeerCluster{ApiPublicUrl: "https://partner.example.com", ClusterName: "cl-deleg"}
	pm.PeerUserClusters["me@sig"] = map[string]*PeerCluster{"h1": delegated}

	// For-sale catalog cluster: present in the catalog (PeerForSale/PeerClusters)
	// but keyed under the seller, NOT in my user set.
	forsale := &PeerCluster{ApiPublicUrl: "https://seller.example.com", ClusterName: "cl-sale"}
	pm.PeerForSale["h2"] = forsale
	pm.PeerClusters["h2"] = forsale

	urls := pm.relevantPeerURLs("me@sig", nil)

	if !urls["https://partner.example.com"] {
		t.Fatalf("delegated peer should be pollable, got %v", urls)
	}
	if urls["https://seller.example.com"] {
		t.Fatalf("for-sale catalog peer must NOT be pollable, got %v", urls)
	}
	if len(urls) != 1 {
		t.Fatalf("expected exactly 1 relevant url, got %d: %v", len(urls), urls)
	}
}

func TestRelevantPeerURLsFreshBrowserPollsNothing(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://fresh.example.com")

	// A catalog full of for-sale clusters, none related to me.
	for i, u := range []string{"https://s1.example.com", "https://s2.example.com"} {
		pc := &PeerCluster{ApiPublicUrl: u}
		key := string(rune('a' + i))
		pm.PeerForSale[key] = pc
		pm.PeerClusters[key] = pc
	}

	if urls := pm.relevantPeerURLs("me@sig", nil); len(urls) != 0 {
		t.Fatalf("fresh browser must poll zero sellers, got %v", urls)
	}
}

func TestRelevantPeerURLsIncludesActiveSessionUsers(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// Delegated to a sub-user who is in an active session; registering user owns nothing.
	sub := &PeerCluster{ApiPublicUrl: "https://subdeleg.example.com"}
	pm.PeerUserClusters["sub@sig"] = map[string]*PeerCluster{"h3": sub}

	urls := pm.relevantPeerURLs("me@sig", []string{"sub@sig"})
	if !urls["https://subdeleg.example.com"] {
		t.Fatalf("active-session user's delegated peer should be pollable, got %v", urls)
	}
}

func TestRelevantPeerURLsExcludesOwnApiURL(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// My own instance appears in my user set — must not poll myself.
	own := &PeerCluster{ApiPublicUrl: "https://me.example.com"}
	remote := &PeerCluster{ApiPublicUrl: "https://other.example.com"}
	pm.PeerUserClusters["me@sig"] = map[string]*PeerCluster{"h4": own, "h5": remote}

	urls := pm.relevantPeerURLs("me@sig", nil)
	if urls["https://me.example.com"] {
		t.Fatalf("own ApiURL must be excluded, got %v", urls)
	}
	if !urls["https://other.example.com"] {
		t.Fatalf("remote peer should be pollable, got %v", urls)
	}
}

// ReloadUsers is what wires a cluster into the pollable set. A pure catalog for-sale
// cluster (only the seller's local admin in its ACL) lands in PeerForSale and is not
// pollable by me; a cluster where I hold a pending/sponsor role is demoted OUT of
// PeerForSale and INTO my pollable set — the sale workflow.

func TestReloadUsersCatalogForSaleNotPollableByBrowser(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	pc := &PeerCluster{
		ApiPublicUrl:                   "https://seller.example.com",
		ClusterName:                    "cl-sale",
		Cloud18Shared:                  true,
		ApiCredentialsAclAllowExternal: "admin:x:cl-sale", // only the seller's local admin
	}
	pm.PeerClusters[GetPeerHashID(pc)] = pc
	pm.ReloadUsers(pc)

	if _, ok := pm.PeerForSale[GetPeerHashID(pc)]; !ok {
		t.Fatalf("catalog for-sale cluster should be in PeerForSale")
	}
	if urls := pm.relevantPeerURLs("me@sig", nil); len(urls) != 0 {
		t.Fatalf("a browser (me@sig) must not be able to poll a catalog for-sale cluster, got %v", urls)
	}
}

func TestReloadUsersPendingSaleWorkflowBecomesPollable(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// I (me@sig) requested this for-sale cluster → I hold a pending role on it.
	pc := &PeerCluster{
		ApiPublicUrl:                   "https://seller.example.com",
		ClusterName:                    "cl-sale",
		Cloud18Shared:                  true,
		ApiCredentialsAclAllowExternal: "me@sig:x:cl-sale:pending",
	}
	pm.PeerClusters[GetPeerHashID(pc)] = pc
	pm.ReloadUsers(pc)

	// pending demotes it out of the for-sale catalog...
	if _, ok := pm.PeerForSale[GetPeerHashID(pc)]; ok {
		t.Fatalf("a cluster with a pending user must be demoted OUT of PeerForSale")
	}
	// ...and into my pollable set (I'm watching it become mine).
	if urls := pm.relevantPeerURLs("me@sig", nil); !urls["https://seller.example.com"] {
		t.Fatalf("a cluster I have a pending role on must be pollable, got %v", urls)
	}
}

// Local clusters — those THIS instance manages locally (name in localNames) — are
// excluded from the PEER view for the admin, who already sees them in the local
// dashboard. Exclusion keys on the local cluster NAME, not api-public-url. The
// for-sale view is NEVER filtered this way. A nil/empty localNames excludes nothing.
func TestPeerViewExcludesLocalForAdmin(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// cl-local is one I run locally; cl-deleg is a partner's cluster delegated to me.
	mine := &PeerCluster{ApiPublicUrl: "https://me.example.com", ClusterName: "cl-local"}
	partner := &PeerCluster{ApiPublicUrl: "https://partner.example.com", ClusterName: "cl-deleg"}
	pm.PeerUserClusters["me@sig"] = map[string]*PeerCluster{
		"h-mine":    mine,
		"h-partner": partner,
	}
	local := map[string]bool{"cl-local": true}

	// Admin: local clusters are excluded (localNames set).
	got := pm.GetUserClusters("me@sig", local)
	if len(got) != 1 || got[0].ClusterName != "cl-deleg" {
		t.Fatalf("admin peer view must exclude own local cluster, keep partner's; got %+v", got)
	}

	// Non-admin SSO user: no exclusion (nil set) — a shared local cluster stays visible.
	got = pm.GetUserClusters("me@sig", nil)
	if len(got) != 2 {
		t.Fatalf("non-admin peer view must exclude nothing; got %+v", got)
	}

	// For-sale is never ownership-filtered — every offer is listed.
	pm.PeerForSale["h-mine"] = mine
	pm.PeerForSale["h-sale"] = &PeerCluster{ApiPublicUrl: "https://seller.example.com", ClusterName: "cl-sale"}
	saleJSON, err := pm.GetSaleClustersJSON()
	if err != nil {
		t.Fatalf("GetSaleClustersJSON: %v", err)
	}
	if s := string(saleJSON); !strings.Contains(s, "cl-local") || !strings.Contains(s, "cl-sale") {
		t.Fatalf("for-sale view must list ALL offers incl. own; got %s", s)
	}
}

// A DR standby shares its primary's api-public-url but runs a different (often empty)
// local cluster set. Exclusion keys on the local NAME set — with no local clusters,
// a cluster carrying our api-public-url must STILL be visible in the admin peer view
// (regression: the down clusters vanished on the DR because they shared its api-url).
func TestDRSharedApiUrlDoesNotHideClusters(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://dbaas-fr-2.signal18.io") // DR configured with primary's url

	// Published by the primary, same api-public-url as the DR, but NOT run locally here.
	down := &PeerCluster{ApiPublicUrl: "https://dbaas-fr-2.signal18.io", ClusterName: "goodlands", IsDown: true}
	pm.PeerUserClusters["me@sig"] = map[string]*PeerCluster{"h1": down}

	got := pm.GetUserClusters("me@sig", map[string]bool{}) // DR has no live local clusters
	if len(got) != 1 || got[0].ClusterName != "goodlands" {
		t.Fatalf("a cluster sharing the DR api-url but not run locally must stay visible; got %+v", got)
	}
}

// TestReloadPreservesLiveEnrichmentInViews locks the fix: after a peer.json RELOAD
// (a second BatchUpdateClusters over an existing cluster), the peer view
// (PeerUserClusters) and the marketplace (PeerForSale) must share the SAME object
// that the live /api/clusters poll enriches (PeerClusters[hashID]). Before the fix,
// ReloadUsers was wired to the transient catalog object, so a post-reload live poll
// updated PeerClusters but the views showed a stale sibling → for-sale clusters
// stayed gray/not-provisioned even when the peer reported them live.
func TestReloadPreservesLiveEnrichmentInViews(t *testing.T) {
	pm := NewPeerManager(30)
	mk := func() *PeerCluster {
		return &PeerCluster{
			ApiPublicUrl:                   "https://seller.example.com",
			ClusterName:                    "cl-sale",
			Cloud18Shared:                  true,
			ApiCredentialsAclAllowExternal: "me@sig:x:cl-sale",
		}
	}
	pm.BatchUpdateClusters([]*PeerCluster{mk()}, false) // initial catalog load
	pm.BatchUpdateClusters([]*PeerCluster{mk()}, false) // reload -> existing-cluster path

	hashID := GetHashID("https://seller.example.com", "cl-sale")
	stored, ok := pm.PeerClusters[hashID]
	if !ok {
		t.Fatalf("cluster missing from PeerClusters under %s", hashID)
	}

	// Simulate the live /api/clusters enrichment (GetClusterDetails writes here).
	stored.DirectUpdate = time.Now()
	stored.IsProvisioned = true

	uv, ok := pm.PeerUserClusters["me@sig"][hashID]
	if !ok {
		t.Fatalf("cluster missing from PeerUserClusters[me@sig]")
	}
	if uv.DirectUpdate.IsZero() || !uv.IsProvisioned {
		t.Errorf("peer view did NOT reflect live enrichment after reload: directUpdate=%v isProvisioned=%v (view points at a stale sibling object)", uv.DirectUpdate, uv.IsProvisioned)
	}

	fs, ok := pm.PeerForSale[hashID]
	if !ok {
		t.Fatalf("cluster missing from PeerForSale")
	}
	if fs.DirectUpdate.IsZero() || !fs.IsProvisioned {
		t.Errorf("for-sale view did NOT reflect live enrichment after reload")
	}
}
