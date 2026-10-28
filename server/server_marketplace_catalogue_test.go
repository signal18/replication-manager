package server

import (
	"hash"
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/config"
)

// The back-office's catalogue is read from .pull like partners.json: again when a file
// changes, kept when a new version does not decode, emptied when the file is gone.
func TestLoadMarketplaceCatalogue(t *testing.T) {
	dir := t.TempDir()
	pull := filepath.Join(dir, ".pull")
	if err := os.MkdirAll(pull, 0755); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{
		Conf:           &config.Config{WorkingDir: dir},
		ModTimes:       map[string]time.Time{},
		CheckSumConfig: map[string]hash.Hash{},
		Logrus:         log.New(),
	}
	write := func(name, content string, at time.Time) {
		t.Helper()
		p := filepath.Join(pull, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}

	// a back-office that pushes nothing yet: empty, no error
	repman.LoadMarketplaceCatalogue()
	if c := repman.MarketplaceCatalogue(); c.S3 != nil || c.Archives != nil {
		t.Fatalf("no file: %+v", c)
	}

	at := time.Now().Add(-time.Hour)
	write("marketplace-s3.json", `[{"name":"s3-fr-1-sso","partner":"partner-a","providerSource":"custom","endpoint":"https://s3.example","region":"eu-west-1","cloud18Sso":true,"auth":"cloud18-sso"}]`, at)
	write("marketplace-archive.json", `[{"name":"pbs-simple","partner":"partner-b","plugin":"archive-pbs","s3Provider":null,"auth":"partner-token","sizeUnit":"TB","priceEurMonth":12,"priceEurBauMonth":0.2577,"retrievalCostEurGib":null}]`, at)
	repman.LoadMarketplaceCatalogue()
	c := repman.MarketplaceCatalogue()
	if len(c.S3) != 1 || !c.S3[0].Cloud18SSO || c.S3[0].ProviderSource != config.S3ProviderSourceCustom {
		t.Fatalf("s3 = %+v", c.S3)
	}
	if len(c.Archives) != 1 || c.Archives[0].S3Provider != "" || *c.Archives[0].PriceEurBauMonth != 0.2577 || c.Archives[0].RetrievalCostEurGib != nil {
		t.Fatalf("archives = %+v", c.Archives)
	}

	// a new version that does not decode keeps the list read before
	write("marketplace-archive.json", `[{"name":`, at.Add(time.Minute))
	repman.LoadMarketplaceCatalogue()
	if c := repman.MarketplaceCatalogue(); len(c.Archives) != 1 || c.Archives[0].Name != "pbs-simple" {
		t.Fatalf("a broken file must keep the list: %+v", c.Archives)
	}
	// a good version replaces it
	write("marketplace-archive.json", `[{"name":"a","partner":"p","plugin":"archive-plakar-s3","s3Provider":"a","auth":"cloud18-keys","sizeUnit":"TiB"},{"name":"b","partner":"p","plugin":"archive-pbs","auth":"partner-token","sizeUnit":"TB"}]`, at.Add(2*time.Minute))
	repman.LoadMarketplaceCatalogue()
	if c := repman.MarketplaceCatalogue(); len(c.Archives) != 2 || c.Archives[0].S3Provider != "a" || c.Archives[0].PriceEurMonth != nil {
		t.Fatalf("new version: %+v", c.Archives)
	}
	// the back-office stops pushing the S3 list: emptied, the archives stay
	if err := os.Remove(filepath.Join(pull, "marketplace-s3.json")); err != nil {
		t.Fatal(err)
	}
	repman.LoadMarketplaceCatalogue()
	if c := repman.MarketplaceCatalogue(); c.S3 != nil || len(c.Archives) != 2 {
		t.Fatalf("file gone: %+v", c)
	}
}

// A file above the size bound is refused and the list read before stays; a file removed and
// pushed again with the same modification time is read again.
func TestLoadMarketplaceCatalogueBounds(t *testing.T) {
	dir := t.TempDir()
	pull := filepath.Join(dir, ".pull")
	if err := os.MkdirAll(pull, 0755); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{
		Conf:           &config.Config{WorkingDir: dir},
		ModTimes:       map[string]time.Time{},
		CheckSumConfig: map[string]hash.Hash{},
		Logrus:         log.New(),
	}
	p := filepath.Join(pull, "marketplace-s3.json")
	at := time.Now().Add(-time.Hour)
	put := func(content []byte, when time.Time) {
		t.Helper()
		if err := os.WriteFile(p, content, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	good := []byte(`[{"name":"s3-a","partner":"p","providerSource":"custom","cloud18Sso":false,"auth":"cloud18-keys"}]`)
	put(good, at)
	repman.LoadMarketplaceCatalogue()
	if len(repman.MarketplaceCatalogue().S3) != 1 {
		t.Fatal("first read")
	}
	big := make([]byte, pullJSONMaxBytes+1)
	put(big, at.Add(time.Minute))
	repman.LoadMarketplaceCatalogue()
	if c := repman.MarketplaceCatalogue(); len(c.S3) != 1 || c.S3[0].Name != "s3-a" {
		t.Fatalf("an oversized file must keep the list: %+v", c.S3)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	repman.LoadMarketplaceCatalogue()
	if repman.MarketplaceCatalogue().S3 != nil {
		t.Fatal("a removed file empties its list")
	}
	put(good, at) // pushed again, same modification time as the first read
	repman.LoadMarketplaceCatalogue()
	if len(repman.MarketplaceCatalogue().S3) != 1 {
		t.Fatal("a file pushed again must be read again")
	}
}

// #2227: the vault offers are read from .pull/marketplace-vault.json, and each registered
// cluster gets the offer as a vault entry, off, jwt, at the instance's URI
func TestLoadMarketplaceVaultOffers(t *testing.T) {
	dir := t.TempDir()
	pull := filepath.Join(dir, ".pull")
	if err := os.MkdirAll(pull, 0755); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{
		Conf:           &config.Config{WorkingDir: dir},
		ModTimes:       map[string]time.Time{},
		CheckSumConfig: map[string]hash.Hash{},
		Logrus:         log.New(),
	}
	if err := os.WriteFile(filepath.Join(pull, "marketplace-vault.json"), []byte(`[{"name":"vault-fr","partner":"partner-a","address":"https://vault.example:8200"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	repman.LoadMarketplaceCatalogue()
	c := repman.MarketplaceCatalogue()
	if len(c.Vaults) != 1 || c.Vaults[0].Address != "https://vault.example:8200" {
		t.Fatalf("vaults = %+v", c.Vaults)
	}
	conf := &config.Config{ImmuableFlagMap: map[string]interface{}{}, DefaultFlagMap: map[string]interface{}{}}
	changed, err := conf.EnsureMarketplaceVault(c.Vaults[0], "d/s-fr")
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	e := conf.SecretKvs[0]
	if e.Name != "cloud18-vault-fr" || e.Enabled || e.Settings["auth"] != "jwt" || e.Settings["path-prefix"] != "d/s-fr" || e.Settings["mount"] != "kv" {
		t.Fatalf("entry %+v", e)
	}
	// the sponsor turns it on; the offer read again leaves the switch alone
	conf.SetSecretKvEnabled(e.Name, true)
	if changed, _ := conf.EnsureMarketplaceVault(c.Vaults[0], "d/s-fr"); changed || !conf.SecretKvs[0].Enabled {
		t.Fatalf("read again: changed %v, entry %+v", changed, conf.SecretKvs[0])
	}
}
