// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A storage reached with the Cloud18 SSO is usable at once on a connected replication-manager; a
// storage whose keys Cloud18 issues is requestable, requested, then usable once delivered; a
// partner-token offer is on request; an added storage is marked.
func TestArchiveOffers(t *testing.T) {
	price := 0.2344
	cat := config.MarketplaceCatalogue{
		S3: []config.MarketplaceS3{
			{Name: "s3-sso", Endpoint: "https://s3.example", Region: "fr-1", Bucket: "cloud18", Cloud18SSO: true, Auth: "cloud18-sso"},
			{Name: "s3-sso-nobucket", Endpoint: "https://s3b.example", Region: "fr-2", Cloud18SSO: true, Auth: "cloud18-sso"},
			{Name: "s3-keys", Endpoint: "https://s3.ovh.example", Region: "gra", Auth: "cloud18-keys"},
		},
		Archives: []config.MarketplaceArchiveOffer{
			{Name: "s3-sso", Plugin: "archive-plakar-s3", S3Provider: "s3-sso", Auth: "cloud18-sso", PriceEurBauMonth: &price},
			{Name: "s3-keys", Plugin: "archive-plakar-s3", S3Provider: "s3-keys", Auth: "cloud18-keys"},
			{Name: "pbs", Plugin: "archive-pbs", Auth: "partner-token"},
		},
	}
	got := archiveOffers(cat, []config.S3Provider{{Name: "s3-sso"}}, archiveOfferRequests(""), nil)
	if len(got) != 3 {
		t.Fatalf("one row per offer: %d", len(got))
	}
	if o := got[0]; !o.Usable || !o.Added || o.S3 == nil || o.S3.Endpoint != "https://s3.example" || o.Reason != "" || o.PriceEurBauMonth == nil {
		t.Fatalf("an SSO storage is usable, added, with its storage and price: %+v", o)
	}
	if o := got[1]; o.Usable || o.Added || o.S3 == nil || !o.Requestable || o.Requested || o.Reason != archiveOfferRequest {
		t.Fatalf("a storage with keys issued by Cloud18 is requestable: %+v", o)
	}
	if o := archiveOffers(cat, nil, archiveOfferRequests(" pbs, s3-keys "), nil)[1]; o.Usable || o.Requestable || !o.Requested || o.Reason != archiveOfferRequested {
		t.Fatalf("a requested storage waits for its keys: %+v", o)
	}
	if o := archiveOffers(cat, []config.S3Provider{{Name: "s3-keys"}}, archiveOfferRequests("s3-keys"), nil)[1]; !o.Usable || !o.Added || o.Reason != "" {
		t.Fatalf("a delivered storage is usable: %+v", o)
	}
	if o := got[2]; o.Usable || o.S3 != nil || o.Requestable || o.Reason != archiveOfferOnRequest {
		t.Fatalf("a partner-token offer is on request: %+v", o)
	}
	notConnected := errors.New("not connected to Cloud18")
	if o := archiveOffers(cat, nil, nil, notConnected)[0]; o.Usable || o.Reason != notConnected.Error() {
		t.Fatalf("an SSO storage on a replication-manager not connected says why: %+v", o)
	}
	noBucket := config.MarketplaceCatalogue{S3: cat.S3, Archives: []config.MarketplaceArchiveOffer{{Name: "nb", S3Provider: "s3-sso-nobucket", Auth: "cloud18-sso"}}}
	if o := archiveOffers(noBucket, nil, nil, nil)[0]; o.Usable || o.Reason != archiveOfferNoBucket {
		t.Fatalf("an SSO storage the catalogue gives no bucket for is not usable: %+v", o)
	}
	if len(archiveOffers(config.MarketplaceCatalogue{}, nil, nil, nil)) != 0 {
		t.Fatal("no catalogue, no offer")
	}
}
