package splitdump

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSplitDumpManifestCoversEveryShard reproduces the "restore skips .00000"
// class of bug at the source: after a split, EVERY data shard written to disk
// (.00000, .00001, …) must be recorded in the manifest — because the manifest-
// based restore only loads what the manifest lists. Feeds several tables,
// including one large enough to split into multiple shards.
func TestSplitDumpManifestCoversEveryShard(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(4096) // small → force the big table to split
	go SplitDumpLineParser(bus, outputDir, SplitDumpOptions{StreamSizeMax: &streamSizeMax})

	feed := func(lines ...string) {
		for _, l := range lines {
			bus.CurrentLine <- l
		}
	}
	feed("USE `mydb`\n")
	// small single-shard table
	feed(
		"-- Table structure for table `small`\n",
		"CREATE TABLE `small` (id int)\n",
		"-- Dumping data for table `small`\n",
		"LOCK TABLES `small` WRITE;\n",
		"INSERT INTO `small` VALUES (-999),(1),(2);\n",
		"UNLOCK TABLES;\n",
	)
	// large table that must split into several shards
	feed(
		"-- Table structure for table `big`\n",
		"CREATE TABLE `big` (id int)\n",
		"-- Dumping data for table `big`\n",
		"LOCK TABLES `big` WRITE;\n",
	)
	for i := 0; i < 400; i++ {
		feed("INSERT INTO `big` VALUES (" + strings.Repeat("9,", 20) + "9);\n")
	}
	feed("UNLOCK TABLES;\n")
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for splitdump")
	}

	// Every data shard file on disk must be listed in the manifest.
	diskShards, _ := filepath.Glob(filepath.Join(outputDir, "*.[0-9][0-9][0-9][0-9][0-9].sql.gz"))
	if len(diskShards) == 0 {
		t.Fatal("no shard files were produced")
	}
	m, err := ReadManifest(outputDir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	inManifest := map[string]bool{}
	for _, n := range m.Data {
		inManifest[n] = true
	}
	var missing []string
	for _, p := range diskShards {
		b := filepath.Base(p)
		if !inManifest[b] {
			missing = append(missing, b)
		}
	}
	t.Logf("disk shards=%d, manifest data entries=%d", len(diskShards), len(m.Data))
	if len(missing) > 0 {
		t.Errorf("%d shard file(s) on disk are MISSING from the manifest (restore would skip them):", len(missing))
		for _, b := range missing {
			t.Errorf("  MISSING FROM MANIFEST: %s", b)
		}
	}
}
