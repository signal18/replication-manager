package splitdump

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jacoblockett/sanitizefilename"
)

// SplitDumpChannelBusChannelBus a struct to hold all channels used by the different go routines
type SplitDumpChannelBus struct {
	Finished    chan bool
	Error       chan error
	CurrentLine chan string
}

func NewSplitDumpChannelBus() *SplitDumpChannelBus {
	return &SplitDumpChannelBus{
		Finished:    make(chan bool),
		Error:       make(chan error, 1),
		CurrentLine: make(chan string),
	}
}

func isGzip(b *bufio.Reader) bool {
	if m, err := b.Peek(2); err == nil {
		return m[0] == 0x1f && m[1] == 0x8b
	}
	return false
}

func splitDumpOpenReader(f *os.File) *bufio.Reader {
	pageSize := os.Getpagesize() * 2
	buf := bufio.NewReaderSize(f, pageSize)

	if isGzip(buf) {
		gbuf, err := gzip.NewReader(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "splitdump: warning: gzip header detected but failed to init gzip reader; falling back to raw reader: %v\n", err)
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr == nil {
				return bufio.NewReaderSize(f, pageSize)
			}
			return buf
		}
		return bufio.NewReaderSize(gbuf, pageSize)
	}

	return buf
}
func splitDumpOpenFile(path string) (*os.File, error) {

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func splitDumpOpenReaderStdin() *bufio.Reader {
	pageSize := os.Getpagesize() * 2
	buf := bufio.NewReaderSize(os.Stdin, pageSize)
	return buf
}

// LineReader reads `file` line-by-line and adds it to the `bus.CurrentLine` channel.
// Note: This function closes `file`.
func SplitDumpLineReader(bus *SplitDumpChannelBus, inputFile string) {
	r := splitDumpOpenReaderStdin()
	if inputFile != "" {
		file, err := splitDumpOpenFile(inputFile)
		if err != nil {
			bus.Error <- err
			close(bus.CurrentLine)
			return
		}
		defer file.Close()
		r = splitDumpOpenReader(file)
	}
	for {
		line, err := r.ReadString('\n')
		if err == nil {
			bus.CurrentLine <- line
			continue
		}
		if err == io.EOF {
			if line != "" {
				bus.CurrentLine <- line
			}
			break
		}
		bus.Error <- err
		break
	}

	close(bus.CurrentLine)
}

// LineParser reads the CurrentLine and figures out which channel to put it in.
func SplitDumpLineParser(bus *SplitDumpChannelBus, outputDir string, opts SplitDumpOptions /*, combineFiles bool, outputDir string, skipData []string, skipTables []string*/) {
	var drainOnce sync.Once
	drainCurrentLine := func() {
		drainOnce.Do(func() {
			go func() {
				for range bus.CurrentLine {
				}
			}()
		})
	}
	reportError := func(err error) {
		if err == nil {
			return
		}
		defer func() {
			_ = recover()
		}()
		select {
		case bus.Error <- err:
		default:
		}
	}
	finishWithError := func(err error) {
		drainCurrentLine()
		reportError(err)
		bus.Finished <- true
	}

	onTableScheme, onTableData, pastHeader := false, false, false
	shardpad, schema := "", ""
	shard := 0
	dataTableName := ""
	binlogRegexMariaDB := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegexMariaDB := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)
	binlogRegexMySQL := regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	gtidRegexMySQL := regexp.MustCompile(`GTID_PURGED\s*=\s*(?:\/\*![^*]*\*\/\s*)?'([^']+)'`)
	headerMetaData := fmt.Sprintf("-- Generated with replication-manager on %s\n\n", time.Now())
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		finishWithError(fmt.Errorf("splitdump: create output dir %s: %w", outputDir, err))
		return
	}
	// numTables := 0
	var f *os.File
	var err error
	var tableFile *gzip.Writer
	var bgtid, bfile, bpos string
	sourceDataDisabled := false
	closeTableWriter := func() {
		if tableFile != nil {
			_ = tableFile.Flush()
			_ = tableFile.Close()
			tableFile = nil
		}
	}
	closeTableFile := func() {
		closeTableWriter()
		if f != nil {
			_ = f.Close()
			f = nil
		}
	}

	opts, err = normalizeSplitDumpOptions(opts)
	if err != nil {
		finishWithError(err)
		return
	}
	streamSize := int64(0)
	streamSizeMax := opts.StreamSizeMax
	for line := range bus.CurrentLine {
		if !sourceDataDisabled {
			lowerLine := strings.ToLower(line)
			if strings.Contains(lowerLine, "source-data=0") {
				sourceDataDisabled = true
				bgtid = ""
				bfile = ""
				bpos = ""
			}
		}
		//	fmt.Printf("%s %b %b %b", line, onTableScheme, onTableData)
		streamSize += int64(len([]byte(line)))

		if streamSizeMax > 0 && streamSize > streamSizeMax {
			if tableFile == nil {
				streamSize = 0
			} else {
				shard++
				closeTableFile()
				shardpad = fmt.Sprintf(".%05d", shard)
				baseTableName := dataTableName
				if baseTableName == "" {
					baseTableName = schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table ", "", 1), "`", "", -1))
				}
				tableName := baseTableName + shardpad
				fmt.Printf("Processing table data %s ", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err = os.Create(tablePath)
				if err != nil {
					finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
					return
				}
				tableFile = gzip.NewWriter(f)
				tableFile.Write([]byte(headerMetaData))
				tableFile.Write([]byte("\n--\n" + line))
				tableFile.Write([]byte("USE " + schema + ";\n"))
				streamSize = 0
			}
		}
		// The beginning of a mysqldump has some flags at the top of the file. Capture them into a variable.
		if !pastHeader && strings.HasPrefix(line, "/*!40") {
			headerMetaData += line
		}
		// Optimize for no test string durin table dump
		if onTableData {
			if strings.HasPrefix(line, "UNLOCK TABLES;") {
				onTableData = false
				onTableScheme = false
				dataTableName = ""
				streamSize = 0
				closeTableWriter()
			}
		} else {
			if strings.HasPrefix(line, "USE ") {
				schema = strings.TrimSpace(strings.Replace(strings.Replace(strings.Replace(line, "USE ", "", 1), ";", "", -1), "`", "", -1))
			} else if strings.HasPrefix(line, "-- Table structure for table") {
				//case for empty table enter dummy data
				if onTableScheme && !onTableData {
					onTableScheme = false
					// add headers to each file unless we are combining all of them into 1 file.
					closeTableFile()
				}
				onTableScheme, onTableData = true, false
				tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Table structure for table ", "", 1), "`", "", -1)) + "-schema"
				fmt.Printf("Processing table schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err = os.Create(tablePath)
				if err != nil {
					finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
					return
				}
				tableFile = gzip.NewWriter(f)
				pastHeader = true
			} else if strings.HasPrefix(line, "LOCK TABLES `") {
				onTableData, onTableScheme = true, false
			} else if strings.HasPrefix(line, "-- Dumping data for table") {
				// Case of data without table definition
				if !onTableScheme {
					closeTableWriter()
					tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table", "", 1), "`", "", -1)) + "-schema"
					fmt.Printf("Processing table schema %s\n", tableName)
					tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
					f, err = os.Create(tablePath)
					if err != nil {
						finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
						return
					}
					tableFile = gzip.NewWriter(f)
					tableFile.Write([]byte("\n--\n" + line))
					pastHeader = true

				}
				closeTableFile()
				shardpad = fmt.Sprintf(".%05d", shard)
				dataTableName = schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table ", "", 1), "`", "", -1))
				tableName := dataTableName + shardpad
				fmt.Printf("Processing table data %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err = os.Create(tablePath)
				if err != nil {
					finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
					return
				}
				tableFile = gzip.NewWriter(f)

				if !pastHeader {
					// add the meta data to only the first table.
					tableFile.Write([]byte(headerMetaData))
				}
				onTableScheme = false
				onTableData = true

			} else if strings.HasPrefix(line, "-- Final view structure for view ") {
				onTableData = false
				onTableScheme = false
				dataTableName = ""
				//	onView = true
				closeTableFile()
				tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Final view structure for view ", "", 1), "`", "", -1)) + "-schema-view"
				fmt.Printf("Processing view schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err = os.Create(tablePath)
				if err != nil {
					finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
					return
				}
				tableFile = gzip.NewWriter(f)
				//-- Dumping routines
			} else if strings.HasPrefix(line, "INSTALL PLUGIN") || strings.HasPrefix(line, "CREATE USER") {
				onTableData = false
				onTableScheme = false
				dataTableName = ""
				closeTableFile()
				tableName := "mysql.system-all"
				fmt.Printf("Processing system schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err = os.Create(tablePath)
				if err != nil {
					finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
					return
				}
				tableFile = gzip.NewWriter(f)
			}
			if !sourceDataDisabled {
				if matches := gtidRegexMariaDB.FindStringSubmatch(line); matches != nil {
					bgtid = matches[1]
				}
				if matches := gtidRegexMySQL.FindStringSubmatch(line); matches != nil {
					bgtid = matches[1]
				}
				if matches := binlogRegexMariaDB.FindStringSubmatch(line); matches != nil {
					bfile = matches[1]
					bpos = matches[2]
				}
				if matches := binlogRegexMySQL.FindStringSubmatch(line); matches != nil {
					bfile = matches[1]
					bpos = matches[2]
				}
			}
		}

		if pastHeader {
			if tableFile != nil {
				tableFile.Write([]byte(line))
			}
		}

	}
	closeTableFile()
	tablePath := filepath.Join(outputDir, "metadata")
	f, err = os.Create(tablePath)
	if err != nil {
		finishWithError(fmt.Errorf("splitdump: create file %s: %w", tablePath, err))
		return
	}
	metaLines := []string{"[source]", "# Channel_Name = ''"}
	if sourceDataDisabled {
		metaLines = append(metaLines, "Source_Data = 0")
	}
	metaLines = append(metaLines, fmt.Sprintf("File = %s", bfile))
	metaLines = append(metaLines, fmt.Sprintf("Position = %s", bpos))
	metaLines = append(metaLines, fmt.Sprintf("Executed_Gtid_Set = %s", bgtid), "")
	line := strings.Join(metaLines, "\n")
	_, _ = f.Write([]byte(line))
	f.Close()

	bus.Finished <- true
}
