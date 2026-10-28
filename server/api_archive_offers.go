// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ArchiveOffer is an archive offer of the marketplace catalogue as a cluster sees it: the
// offer, its S3 storage when it is one, and whether the cluster can use it now.
type ArchiveOffer struct {
	config.MarketplaceArchiveOffer
	// S3 is the catalogue's storage of an S3 offer (MarketplaceArchiveOffer.S3Provider)
	S3 *config.MarketplaceS3 `json:"s3,omitempty"`
	// Added: the cluster has the offer's storage: an S3 provider of its name, on its endpoint (and
	// reached with the Cloud18 SSO for an SSO storage)
	Added bool `json:"added"`
	// Usable: the cluster can add an archive on the offer's storage now: a storage reached with
	// the Cloud18 SSO on a replication-manager connected to Cloud18 (no key), or a storage whose
	// keys the back-office delivered (the S3 provider is there)
	Usable bool `json:"usable"`
	// Requestable: the back-office delivers the storage's keys to the cluster on request
	// (cloud18-archive-offer-requests); Requested: asked, not delivered yet
	Requestable bool `json:"requestable"`
	Requested   bool `json:"requested"`
	// Reason: why the offer is not usable now
	Reason string `json:"reason,omitempty"`
}

const (
	// archiveOfferAuthKeys: the back-office issues the cluster's own keys (its bucket, its user)
	archiveOfferAuthKeys = "cloud18-keys"
	// an offer whose access nobody delivers yet (a partner token)
	archiveOfferOnRequest = "on request: Cloud18 delivers this storage's access to the cluster"
	archiveOfferRequest   = "request it: Cloud18 creates the cluster's bucket and keys"
	archiveOfferRequested = "requested: Cloud18 creates the cluster's bucket and keys, then they arrive with the cluster's config"
	archiveOfferNoBucket  = "not yet: the catalogue gives no bucket for this storage"
	archiveOfferConflict  = "an S3 provider of this name is another storage: rename it to use the offer"
	archiveOfferPinned    = "the S3 providers are set in the configuration file (s3-provider): add this one there"
)

// archiveOfferRequestsMax bounds cloud18-archive-offer-requests: offer names, each a catalogue name.
const archiveOfferRequestsMax = 16

var archiveOfferNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// validArchiveOfferRequests checks a value of cloud18-archive-offer-requests: at most
// archiveOfferRequestsMax offer names, comma-separated, each a plain catalogue name.
func validArchiveOfferRequests(value string) error {
	set := archiveOfferRequests(value)
	if len(set) > archiveOfferRequestsMax {
		return fmt.Errorf("cloud18-archive-offer-requests: %d offers, at most %d", len(set), archiveOfferRequestsMax)
	}
	for name := range set {
		if !archiveOfferNameRe.MatchString(name) {
			return fmt.Errorf("cloud18-archive-offer-requests: %q is not an offer name", name)
		}
	}
	return nil
}

// normalizedArchiveOfferRequests is the value kept: the names once each, sorted.
func normalizedArchiveOfferRequests(value string) string {
	return strings.Join(slices.Sorted(maps.Keys(archiveOfferRequests(value))), ",")
}

// archiveOfferRequests is the set of the offers cloud18-archive-offer-requests asks for.
func archiveOfferRequests(setting string) map[string]bool {
	set := map[string]bool{}
	for _, name := range strings.Split(setting, ",") {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = true
		}
	}
	return set
}

// archiveOffers joins the catalogue's archive offers with their S3 storages, the cluster's S3
// providers and the offers it requested. ssoReady is the cluster's Cloud18SSOReady: why a
// storage reached with the Cloud18 SSO cannot be used here, nil when it can. pinned: a
// configuration file sets the provider list (S3ProvidersPinned), so neither Use nor a delivery
// can add one.
func archiveOffers(cat config.MarketplaceCatalogue, providers []config.S3Provider, requested map[string]bool, ssoReady error, pinned bool) []ArchiveOffer {
	storages := make(map[string]config.MarketplaceS3, len(cat.S3))
	for _, s := range cat.S3 {
		if _, dup := storages[s.Name]; !dup { // remote data: the first of a duplicated name
			storages[s.Name] = s
		}
	}
	have := make(map[string]config.S3Provider, len(providers))
	for _, p := range providers {
		have[p.Name] = p
	}
	offers := make([]ArchiveOffer, 0, len(cat.Archives))
	for _, o := range cat.Archives {
		row := ArchiveOffer{MarketplaceArchiveOffer: o}
		if s, ok := storages[o.S3Provider]; ok && o.S3Provider != "" {
			row.S3 = &s
		}
		conflict := false
		if row.S3 != nil {
			if p, ok := have[row.S3.Name]; ok {
				// the offer's storage only: same endpoint, and the SSO for an SSO storage
				row.Added = strings.TrimRight(p.Endpoint, "/") == strings.TrimRight(row.S3.Endpoint, "/") && p.Cloud18SSO == row.S3.Cloud18SSO
				conflict = !row.Added
			}
		}
		switch {
		case conflict:
			row.Reason = archiveOfferConflict
		case pinned && row.S3 != nil && !row.Added:
			row.Reason = archiveOfferPinned
		case row.S3 != nil && row.S3.Cloud18SSO:
			switch {
			case ssoReady != nil:
				row.Reason = ssoReady.Error()
			case row.S3.Bucket == "":
				row.Reason = archiveOfferNoBucket
			default:
				row.Usable = true
			}
		case row.S3 != nil && o.Auth == archiveOfferAuthKeys:
			row.Requested = requested[o.Name]
			switch {
			case row.Added: // delivered: the back-office's S3 provider holds the keys
				row.Usable = true
			case row.Requested:
				row.Reason = archiveOfferRequested
			default:
				row.Requestable = true
				row.Reason = archiveOfferRequest
			}
		default:
			row.Reason = archiveOfferOnRequest
		}
		offers = append(offers, row)
	}
	return offers
}

// handlerMuxArchiveOffers lists the marketplace's archive offers for the cluster.
// @Summary List the archive offers
// @Description The archive offers of the marketplace catalogue the back-office pushes to every instance, each with its S3 storage, whether the cluster already has that storage and whether it can use the offer now: a storage reached with the Cloud18 SSO needs no key; a storage whose keys Cloud18 issues is requested (cloud18-archive-offer-requests) and usable once its S3 provider is delivered; the others are on request. Public fields only, no key.
// @Tags ClusterArchive
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} ArchiveOffer
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/archive-offers [get]
func (repman *ReplicationManager) handlerMuxArchiveOffers(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		writeArchiveJSON(w, archiveOffers(repman.MarketplaceCatalogue(), mycluster.GetS3ProvidersSnapshot(),
			archiveOfferRequests(mycluster.Conf.Cloud18ArchiveOfferRequests), mycluster.Cloud18SSOReady(), mycluster.S3ProvidersPinned()))
	})
}
