// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"net/http"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ArchiveOffer is an archive offer of the marketplace catalogue as a cluster sees it: the
// offer, its S3 storage when it is one, and whether the cluster can use it now.
type ArchiveOffer struct {
	config.MarketplaceArchiveOffer
	// S3 is the catalogue's storage of an S3 offer (MarketplaceArchiveOffer.S3Provider)
	S3 *config.MarketplaceS3 `json:"s3,omitempty"`
	// Added: the cluster has an S3 provider of the offer's storage name
	Added bool `json:"added"`
	// Usable: the cluster can add the storage and an archive on it now, with no key to
	// deliver (a storage reached with the Cloud18 SSO, on a replication-manager connected to Cloud18)
	Usable bool `json:"usable"`
	// Reason: why the offer is not usable now
	Reason string `json:"reason,omitempty"`
}

// The offers whose access the back-office delivers per cluster (keys, a partner token) are
// shown on request: that delivery is not there yet.
const archiveOfferOnRequest = "on request: Cloud18 delivers this storage's access to the cluster"

// archiveOffers joins the catalogue's archive offers with their S3 storages and the cluster's
// S3 providers. ssoReady is the cluster's Cloud18SSOReady: why a storage reached with the
// Cloud18 SSO cannot be used here, nil when it can.
func archiveOffers(cat config.MarketplaceCatalogue, providers []config.S3Provider, ssoReady error) []ArchiveOffer {
	storages := make(map[string]config.MarketplaceS3, len(cat.S3))
	for _, s := range cat.S3 {
		storages[s.Name] = s
	}
	have := make(map[string]bool, len(providers))
	for _, p := range providers {
		have[p.Name] = true
	}
	offers := make([]ArchiveOffer, 0, len(cat.Archives))
	for _, o := range cat.Archives {
		row := ArchiveOffer{MarketplaceArchiveOffer: o}
		if s, ok := storages[o.S3Provider]; ok && o.S3Provider != "" {
			s := s
			row.S3 = &s
			row.Added = have[s.Name]
		}
		switch {
		case row.S3 == nil || !row.S3.Cloud18SSO:
			row.Reason = archiveOfferOnRequest
		case ssoReady != nil:
			row.Reason = ssoReady.Error()
		default:
			row.Usable = true
		}
		offers = append(offers, row)
	}
	return offers
}

// handlerMuxArchiveOffers lists the marketplace's archive offers for the cluster.
// @Summary List the archive offers
// @Description The archive offers of the marketplace catalogue the back-office pushes to every instance, each with its S3 storage, whether the cluster already has that storage and whether it can use the offer now: a storage reached with the Cloud18 SSO needs no key, the others are on request. Public fields only, no key.
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
		writeArchiveJSON(w, archiveOffers(repman.MarketplaceCatalogue(), mycluster.GetS3ProvidersSnapshot(), mycluster.Cloud18SSOReady()))
	})
}
