// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"fmt"
	"strings"
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
	got := archiveOffers(cat, []config.S3Provider{{Name: "s3-sso", Endpoint: "https://s3.example/", Cloud18SSO: true}}, archiveOfferRequests(""), nil, false)
	if len(got) != 3 {
		t.Fatalf("one row per offer: %d", len(got))
	}
	if o := got[0]; !o.Usable || !o.Added || o.S3 == nil || o.S3.Endpoint != "https://s3.example" || o.Reason != "" || o.PriceEurBauMonth == nil {
		t.Fatalf("an SSO storage is usable, added, with its storage and price: %+v", o)
	}
	if o := got[1]; o.Usable || o.Added || o.S3 == nil || !o.Requestable || o.Requested || o.Reason != archiveOfferRequest {
		t.Fatalf("a storage with keys issued by Cloud18 is requestable: %+v", o)
	}
	if o := archiveOffers(cat, nil, archiveOfferRequests(" pbs, s3-keys "), nil, false)[1]; o.Usable || o.Requestable || !o.Requested || o.Reason != archiveOfferRequested {
		t.Fatalf("a requested storage waits for its keys: %+v", o)
	}
	if o := archiveOffers(cat, []config.S3Provider{{Name: "s3-keys", Endpoint: "https://s3.ovh.example"}}, archiveOfferRequests("s3-keys"), nil, false)[1]; !o.Usable || !o.Added || o.Reason != "" {
		t.Fatalf("a delivered storage is usable: %+v", o)
	}
	// a provider of the offer's name that is another storage: a conflict, never used as the offer
	for _, other := range []config.S3Provider{{Name: "s3-sso", Endpoint: "https://elsewhere.example"}, {Name: "s3-sso", Endpoint: "https://s3.example"}} {
		if o := archiveOffers(cat, []config.S3Provider{other}, nil, nil, false)[0]; o.Usable || o.Added || o.Reason != archiveOfferConflict {
			t.Fatalf("another storage of the offer's name %+v: %+v", other, o)
		}
	}
	dup := config.MarketplaceCatalogue{S3: append([]config.MarketplaceS3{{Name: "s3-sso", Endpoint: "https://first.example", Bucket: "b", Cloud18SSO: true}}, cat.S3...), Archives: cat.Archives[:1]}
	if o := archiveOffers(dup, nil, nil, nil, false)[0]; o.S3 == nil || o.S3.Endpoint != "https://first.example" {
		t.Fatalf("a duplicated storage name keeps the first: %+v", o.S3)
	}
	if o := got[2]; o.Usable || o.S3 != nil || o.Requestable || o.Reason != archiveOfferOnRequest {
		t.Fatalf("a partner-token offer is on request: %+v", o)
	}
	// a provider list set in the configuration file: no Use, no Request, an added one stays usable
	for i, o := range archiveOffers(cat, []config.S3Provider{{Name: "s3-sso", Endpoint: "https://s3.example", Cloud18SSO: true}}, nil, nil, true) {
		if i < 2 && (o.Requestable || (i == 1 && (o.Usable || o.Reason != archiveOfferPinned))) || (i == 0 && !o.Usable) {
			t.Fatalf("pinned providers, offer %d: %+v", i, o)
		}
	}
	notConnected := errors.New("not connected to Cloud18")
	if o := archiveOffers(cat, nil, nil, notConnected, false)[0]; o.Usable || o.Reason != notConnected.Error() {
		t.Fatalf("an SSO storage on a replication-manager not connected says why: %+v", o)
	}
	noBucket := config.MarketplaceCatalogue{S3: cat.S3, Archives: []config.MarketplaceArchiveOffer{{Name: "nb", S3Provider: "s3-sso-nobucket", Auth: "cloud18-sso"}}}
	if o := archiveOffers(noBucket, nil, nil, nil, false)[0]; o.Usable || o.Reason != archiveOfferNoBucket {
		t.Fatalf("an SSO storage the catalogue gives no bucket for is not usable: %+v", o)
	}
	if len(archiveOffers(config.MarketplaceCatalogue{}, nil, nil, nil, false)) != 0 {
		t.Fatal("no catalogue, no offer")
	}
}

// #2219: cloud18-archive-offer-requests holds at most archiveOfferRequestsMax plain offer names,
// kept once each, sorted.
func TestArchiveOfferRequestsSetting(t *testing.T) {
	if err := validArchiveOfferRequests("ovh-s3-standard-gra, ovh-s3-standard-sbg,ovh-s3-standard-gra"); err != nil {
		t.Fatal(err)
	}
	if got := normalizedArchiveOfferRequests(" b,a,b "); got != "a,b" {
		t.Fatalf("normalized: %q", got)
	}
	for _, bad := range []string{"../x", "Ovh", "a b", strings.Repeat("x", 65)} {
		if validArchiveOfferRequests(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	many := make([]string, archiveOfferRequestsMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("o%d", i)
	}
	if validArchiveOfferRequests(strings.Join(many, ",")) == nil {
		t.Error("too many offers accepted")
	}
}
