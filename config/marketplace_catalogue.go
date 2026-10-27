// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

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
