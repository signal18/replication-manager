// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"strings"
)

// MarketplaceS3 is an S3 storage of the marketplace catalogue the back-office pushes to every
// instance (.pull/marketplace-s3.json). The fields are an S3 provider's (S3Provider) and what
// the catalogue shows about it; public only: no key ever travels in the catalogue.
type MarketplaceS3 struct {
	Name           string           `json:"name"`
	Title          string           `json:"title,omitempty"`
	Partner        string           `json:"partner"`
	ProviderSource S3ProviderSource `json:"providerSource"`
	Endpoint       string           `json:"endpoint,omitempty"`
	Region         string           `json:"region,omitempty"`
	// Bucket: the bucket an archive on this storage uses (S3Provider.Bucket); with the Cloud18
	// SSO, each identity has its own folder in it
	Bucket string `json:"bucket,omitempty"`
	// Cloud18SSO: reached with the Cloud18 identity, no keys (S3Provider.Cloud18SSO)
	Cloud18SSO   bool   `json:"cloud18Sso"`
	Auth         string `json:"auth"` // cloud18-sso, cloud18-keys or partner-token
	StorageClass string `json:"storageClass,omitempty"`
	Dc           string `json:"dc,omitempty"`
	DcLocation   string `json:"dcLocation,omitempty"`
}

// MarketplaceArchiveOffer is an archive offer of the marketplace catalogue
// (.pull/marketplace-archive.json): a partner's storage sold for archives, with its prices.
// A price the back-office does not know is nil, never 0.
type MarketplaceArchiveOffer struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Partner string `json:"partner"`
	Plugin  string `json:"plugin"` // the archive plugin it runs with (archive-plakar-s3, archive-pbs)
	// Requestable: the back-office delivers the cluster's own access on request
	// (cloud18-archive-offer-requests); an offer with keys issued by Cloud18 always is
	Requestable bool `json:"requestable,omitempty"`
	// S3Provider names the MarketplaceS3 entry of an S3 offer; empty for the others
	S3Provider          string   `json:"s3Provider,omitempty"`
	Auth                string   `json:"auth"`
	StorageClass        string   `json:"storageClass,omitempty"`
	Region              string   `json:"region,omitempty"`
	Dc                  string   `json:"dc,omitempty"`
	DcLocation          string   `json:"dcLocation,omitempty"`
	SizeUnit            string   `json:"sizeUnit"` // the unit priceEurMonth is for: GiB, TiB or TB
	PriceEurMonth       *float64 `json:"priceEurMonth"`
	PriceEurBauMonth    *float64 `json:"priceEurBauMonth"` // per BAU (20 GiB) per month
	RetrievalCostEurGib *float64 `json:"retrievalCostEurGib"`
}

// MarketplaceCatalogue is the back-office's catalogue as an instance holds it.
type MarketplaceCatalogue struct {
	S3       []MarketplaceS3           `json:"s3"`
	Archives []MarketplaceArchiveOffer `json:"archives"`
}

// SameStorage: the S3 provider is this catalogue storage: the same endpoint, and the Cloud18
// SSO for an SSO storage.
func (s MarketplaceS3) SameStorage(p S3Provider) bool {
	return strings.TrimRight(p.Endpoint, "/") == strings.TrimRight(s.Endpoint, "/") && p.Cloud18SSO == s.Cloud18SSO
}

// ArchiveOffer is the offer an archive runs on, nil when none: an S3 offer when the archive's
// S3 provider (its s3-provider setting) has the offer's storage name and is that storage
// (SameStorage), the offer of the archive's plugin first when the storage has several, else
// the storage's first (the storage is what is billed, whatever plugin writes it); an offer
// delivered as an archive (no S3 storage, archive-pbs) when the archive has the offer's name
// and plugin. Remote data: the first of a duplicated name wins.
func (c MarketplaceCatalogue) ArchiveOffer(a ArchiveConfig, providers []S3Provider) *MarketplaceArchiveOffer {
	provider := ""
	if v, ok := a.Settings[ConstArchiveSettingS3Provider]; ok && v != nil {
		provider = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	storages := make(map[string]MarketplaceS3, len(c.S3))
	for _, s := range c.S3 {
		if _, dup := storages[s.Name]; !dup {
			storages[s.Name] = s
		}
	}
	var onStorage *MarketplaceArchiveOffer
	for i := range c.Archives {
		o := c.Archives[i]
		if o.S3Provider == "" {
			if o.Name == a.Name && o.Plugin == a.Plugin {
				return &o
			}
			continue
		}
		s, ok := storages[o.S3Provider]
		if !ok || provider != o.S3Provider {
			continue
		}
		for _, p := range providers {
			if p.Name == provider && s.SameStorage(p) {
				if o.Plugin == a.Plugin {
					return &o
				}
				if onStorage == nil {
					onStorage = &o
				}
				break
			}
		}
	}
	return onStorage
}
