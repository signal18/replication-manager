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
