package peer

import (
	"strings"
	"testing"
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

// Own/local clusters (hosted by THIS instance: api-public-url == ours) already
// appear in the LOCAL cluster list, so they must be filtered out of BOTH the peer
// (GetUserClusters) and the for-sale (GetSaleClustersJSON) views to avoid duplicates.
// URL comparison is normalized (scheme/case/trailing-slash insensitive).
func TestPeerViewsExcludeOwnClusters(t *testing.T) {
	pm := NewPeerManager(30)
	pm.SetApiPublicURL("https://me.example.com")

	// Mine: same public URL as this instance (note trailing slash + scheme differ).
	mine := &PeerCluster{ApiPublicUrl: "http://ME.example.com/", ClusterName: "cl-local"}
	// A partner's cluster delegated to me.
	partner := &PeerCluster{ApiPublicUrl: "https://partner.example.com", ClusterName: "cl-deleg"}
	pm.PeerUserClusters["me@sig"] = map[string]*PeerCluster{
		"h-mine":    mine,
		"h-partner": partner,
	}

	got := pm.GetUserClusters("me@sig")
	if len(got) != 1 || got[0].ClusterName != "cl-deleg" {
		t.Fatalf("peer view must exclude own cluster, keep partner's; got %+v", got)
	}

	// For-sale: my own shared cluster must not be listed to myself.
	pm.PeerForSale["h-mine"] = mine
	pm.PeerForSale["h-sale"] = &PeerCluster{ApiPublicUrl: "https://seller.example.com", ClusterName: "cl-sale"}
	saleJSON, err := pm.GetSaleClustersJSON()
	if err != nil {
		t.Fatalf("GetSaleClustersJSON: %v", err)
	}
	if s := string(saleJSON); strings.Contains(s, "cl-local") || !strings.Contains(s, "cl-sale") {
		t.Fatalf("for-sale view must exclude own cluster, keep seller's; got %s", s)
	}
}
