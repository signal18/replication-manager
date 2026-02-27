package splitdump

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitDumpLineParserEmptyInput(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	close(bus.CurrentLine)

	go SplitDumpLineParser(bus, outputDir, SplitDumpOptions{})

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	metadataPath := filepath.Join(outputDir, "metadata")
	if _, err := os.Stat(metadataPath); err != nil {
		t.Fatalf("metadata file missing: %v", err)
	}
}

func TestSplitDumpOpenReaderInvalidGzip(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "bad.gz")
	data := []byte{0x1f, 0x8b}
	if err := os.WriteFile(inputPath, data, 0644); err != nil {
		t.Fatalf("failed to write input file: %v", err)
	}

	file, err := os.Open(inputPath)
	if err != nil {
		t.Fatalf("failed to open input file: %v", err)
	}
	defer file.Close()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to capture stderr: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	reader := splitDumpOpenReader(file)
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close stderr writer: %v", err)
	}
	logged, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("failed to close stderr reader: %v", err)
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("failed to read input file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("unexpected input bytes: %v", got)
	}
	if !strings.Contains(string(logged), "falling back to raw reader") {
		t.Fatalf("expected fallback warning in stderr, got: %s", logged)
	}
}

func TestSplitDumpLineReaderErrorClosesCurrentLine(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	inputPath := filepath.Join(t.TempDir(), "missing.sql")

	go SplitDumpLineReader(bus, inputPath)

	select {
	case err, ok := <-bus.Error:
		if !ok {
			t.Fatal("expected error channel to be open for first read")
		}
		if err == nil {
			t.Fatal("expected error from SplitDumpLineReader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump error")
	}

	select {
	case _, ok := <-bus.CurrentLine:
		if ok {
			t.Fatal("expected CurrentLine channel to be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for CurrentLine channel close")
	}
}

func TestSplitDumpLineParserMySQLGtidPurgedVariants(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "plain",
			line:     "SET @@GLOBAL.GTID_PURGED='01234567-89ab-cdef-0123-456789abcdef:1-10';\n",
			expected: "01234567-89ab-cdef-0123-456789abcdef:1-10",
		},
		{
			name:     "with-comment",
			line:     "SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '01234567-89ab-cdef-0123-456789abcdef:1-20';\n",
			expected: "01234567-89ab-cdef-0123-456789abcdef:1-20",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := NewSplitDumpChannelBus()
			outputDir := filepath.Join(t.TempDir(), "splitdump")

			go SplitDumpLineParser(bus, outputDir, SplitDumpOptions{})
			bus.CurrentLine <- tc.line
			close(bus.CurrentLine)

			select {
			case <-bus.Finished:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for splitdump to finish")
			}

			metadataPath := filepath.Join(outputDir, "metadata")
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatalf("failed to read metadata file: %v", err)
			}
			expectedLine := "Executed_Gtid_Set = " + tc.expected
			if !strings.Contains(string(data), expectedLine) {
				t.Fatalf("expected metadata to contain %q, got: %s", expectedLine, data)
			}
		})
	}
}

func TestSplitDumpLineParserShardsWithCustomStreamSize(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(16 * 1024 * 1024)
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `mytable`\n",
		"CREATE TABLE `mytable` (id int)\n",
		"-- Dumping data for table `mytable`\n",
		"LOCK TABLES `mytable` WRITE;\n",
	}
	baseSize := int64(0)
	for _, line := range lines {
		baseSize += int64(len(line))
		bus.CurrentLine <- line
	}

	insertLine := "INSERT INTO `mytable` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	insertLen := int64(len(insertLine))
	count := int((streamSizeMax-baseSize)/insertLen) + 2
	for i := 0; i < count; i++ {
		bus.CurrentLine <- insertLine
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	shardPath := filepath.Join(outputDir, "mydb.mytable.00001.sql.gz")
	if _, err := os.Stat(shardPath); err != nil {
		t.Fatalf("expected shard file %s: %v", shardPath, err)
	}

	matches, err := filepath.Glob(filepath.Join(outputDir, "*.00001.sql.gz"))
	if err != nil {
		t.Fatalf("failed to glob shard files: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected shard file with .00001 suffix")
	}

	file, err := os.Open(shardPath)
	if err != nil {
		t.Fatalf("failed to open shard file: %v", err)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("failed to read shard gzip: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read shard content: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "USE `mydb`;") {
		t.Fatalf("expected shard content to include USE for mydb")
	}
	if !strings.Contains(contentStr, "INSERT INTO `mytable`") {
		t.Fatalf("expected shard content to include INSERTs for mytable")
	}
	if strings.Index(contentStr, "USE `mydb`;") > strings.Index(contentStr, "INSERT INTO `mytable`") {
		t.Fatalf("expected USE to appear before INSERT in shard content")
	}
}

func TestSplitDumpLineParserNoShardingWhenZero(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(0)
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `mytable`\n",
		"CREATE TABLE `mytable` (id int)\n",
		"-- Dumping data for table `mytable`\n",
		"LOCK TABLES `mytable` WRITE;\n",
	}
	for _, line := range lines {
		bus.CurrentLine <- line
	}

	insertLine := "INSERT INTO `mytable` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	for i := 0; i < 2000; i++ {
		bus.CurrentLine <- insertLine
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	matches, err := filepath.Glob(filepath.Join(outputDir, "*.00001.sql.gz"))
	if err != nil {
		t.Fatalf("failed to glob shard files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no shard files when stream-size-max=0")
	}
}

func TestSplitDumpLineParserResetsShardPerTable(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(1024 * 16)
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `table1`\n",
		"CREATE TABLE `table1` (id int)\n",
		"-- Dumping data for table `table1`\n",
		"LOCK TABLES `table1` WRITE;\n",
	}
	baseSize := int64(0)
	for _, line := range lines {
		baseSize += int64(len(line))
		bus.CurrentLine <- line
	}

	insertLine1 := "INSERT INTO `table1` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	insertLen1 := int64(len(insertLine1))
	count1 := int((streamSizeMax-baseSize)/insertLen1) + 2
	for i := 0; i < count1; i++ {
		bus.CurrentLine <- insertLine1
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"

	lines2 := []string{
		"-- Table structure for table `table2`\n",
		"CREATE TABLE `table2` (id int)\n",
		"-- Dumping data for table `table2`\n",
		"LOCK TABLES `table2` WRITE;\n",
		"INSERT INTO `table2` VALUES (1);\n",
		"UNLOCK TABLES;\n",
	}
	for _, line := range lines2 {
		bus.CurrentLine <- line
	}
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	if _, err := os.Stat(filepath.Join(outputDir, "mydb.table1.00001.sql.gz")); err != nil {
		t.Fatalf("expected shard for table1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "mydb.table2.00000.sql.gz")); err != nil {
		t.Fatalf("expected table2 to start at shard 00000: %v", err)
	}
}

func TestSplitDumpLineParserShardsWithoutUse(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(1024 * 16)
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	lines := []string{
		"-- Table structure for table `nouse`\n",
		"CREATE TABLE `nouse` (id int)\n",
		"-- Dumping data for table `nouse`\n",
		"LOCK TABLES `nouse` WRITE;\n",
	}
	baseSize := int64(0)
	for _, line := range lines {
		baseSize += int64(len(line))
		bus.CurrentLine <- line
	}

	insertLine := "INSERT INTO `nouse` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	insertLen := int64(len(insertLine))
	count := int((streamSizeMax-baseSize)/insertLen) + 2
	for i := 0; i < count; i++ {
		bus.CurrentLine <- insertLine
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	matches, err := filepath.Glob(filepath.Join(outputDir, "*nouse.00001.sql.gz"))
	if err != nil {
		t.Fatalf("failed to glob shard files: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected shard file for table without USE")
	}
	shardPath := matches[0]
	file, err := os.Open(shardPath)
	if err != nil {
		t.Fatalf("failed to open shard file: %v", err)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("failed to read shard gzip: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read shard content: %v", err)
	}
	if strings.Contains(string(content), "USE ``;") {
		t.Fatalf("did not expect empty USE statement in shard")
	}
}

func TestSplitDumpLineParserAlternatingShardSizes(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	streamSizeMax := int64(1024 * 16)
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `t1`\n",
		"CREATE TABLE `t1` (id int)\n",
		"-- Dumping data for table `t1`\n",
		"LOCK TABLES `t1` WRITE;\n",
	}
	baseSize := int64(0)
	for _, line := range lines {
		baseSize += int64(len(line))
		bus.CurrentLine <- line
	}
	insertLine1 := "INSERT INTO `t1` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	insertLen1 := int64(len(insertLine1))
	count1 := int((streamSizeMax-baseSize)/insertLen1) + 2
	for i := 0; i < count1; i++ {
		bus.CurrentLine <- insertLine1
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"

	lines2 := []string{
		"-- Table structure for table `t2`\n",
		"CREATE TABLE `t2` (id int)\n",
		"-- Dumping data for table `t2`\n",
		"LOCK TABLES `t2` WRITE;\n",
		"INSERT INTO `t2` VALUES (1);\n",
		"UNLOCK TABLES;\n",
	}
	for _, line := range lines2 {
		bus.CurrentLine <- line
	}

	lines3 := []string{
		"-- Table structure for table `t3`\n",
		"CREATE TABLE `t3` (id int)\n",
		"-- Dumping data for table `t3`\n",
		"LOCK TABLES `t3` WRITE;\n",
	}
	baseSize = 0
	for _, line := range lines3 {
		baseSize += int64(len(line))
		bus.CurrentLine <- line
	}
	insertLine3 := "INSERT INTO `t3` VALUES (" + strings.Repeat("1,", 50) + "1);\n"
	insertLen3 := int64(len(insertLine3))
	count3 := int((streamSizeMax-baseSize)/insertLen3) + 2
	for i := 0; i < count3; i++ {
		bus.CurrentLine <- insertLine3
	}
	bus.CurrentLine <- "UNLOCK TABLES;\n"
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	if _, err := os.Stat(filepath.Join(outputDir, "mydb.t1.00001.sql.gz")); err != nil {
		t.Fatalf("expected shard for t1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "mydb.t2.00000.sql.gz")); err != nil {
		t.Fatalf("expected t2 to be unsharded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "mydb.t3.00001.sql.gz")); err != nil {
		t.Fatalf("expected shard for t3: %v", err)
	}
}

func TestSplitDumpLineParserNoShardWhenLimitEqualsSize(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `edge`\n",
		"CREATE TABLE `edge` (id int)\n",
		"-- Dumping data for table `edge`\n",
		"LOCK TABLES `edge` WRITE;\n",
	}
	baseSize := int64(0)
	for _, line := range lines {
		baseSize += int64(len(line))
	}

	insertLine := "INSERT INTO `edge` VALUES (1);\n"
	insertLen := int64(len(insertLine))
	streamSizeMax := baseSize + insertLen
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	for _, line := range lines {
		bus.CurrentLine <- line
	}

	bus.CurrentLine <- insertLine
	bus.CurrentLine <- "UNLOCK TABLES;\n"
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	if _, err := os.Stat(filepath.Join(outputDir, "mydb.edge.00001.sql.gz")); err == nil {
		t.Fatal("expected no shard when stream size equals limit")
	}
}

func TestSplitDumpLineParserSplitsAtStatementBoundary(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")

	lines := []string{
		"USE `mydb`\n",
		"-- Table structure for table `edge`\n",
		"CREATE TABLE `edge` (id int)\n",
		"-- Dumping data for table `edge`\n",
		"LOCK TABLES `edge` WRITE;\n",
		"INSERT INTO `edge` VALUES\n",
		"(1),(2),(3)\n",
		"(4),(5),(6);\n",
		"INSERT INTO `edge` VALUES (7);\n",
		"UNLOCK TABLES;\n",
	}
	baseSize := int64(0)
	for i := 0; i < 7; i++ {
		baseSize += int64(len(lines[i]))
	}
	streamSizeMax := baseSize - 1
	options := SplitDumpOptions{StreamSizeMax: &streamSizeMax}

	go SplitDumpLineParser(bus, outputDir, options)

	for _, line := range lines {
		bus.CurrentLine <- line
	}
	close(bus.CurrentLine)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	shardPath := filepath.Join(outputDir, "mydb.edge.00001.sql.gz")
	file, err := os.Open(shardPath)
	if err != nil {
		t.Fatalf("failed to open shard file: %v", err)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("failed to read shard gzip: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read shard content: %v", err)
	}
	contentStr := string(content)
	useIndex := strings.Index(contentStr, "USE `mydb`;")
	if useIndex == -1 {
		t.Fatalf("expected USE statement in shard")
	}
	remaining := contentStr[useIndex+len("USE `mydb`;"):]
	remaining = strings.TrimLeft(remaining, "\r\n")
	firstLineEnd := strings.Index(remaining, "\n")
	firstLine := remaining
	if firstLineEnd != -1 {
		firstLine = remaining[:firstLineEnd]
	}
	if !strings.HasPrefix(firstLine, "INSERT INTO `edge`") {
		t.Fatalf("expected shard to start with INSERT statement, got: %s", firstLine)
	}
}
