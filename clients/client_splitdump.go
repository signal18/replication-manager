//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jacoblockett/sanitizefilename"
	"github.com/spf13/cobra"
)

// SplitDumpChannelBusChannelBus a struct to hold all channels used by the different go routines
type SplitDumpChannelBus struct {
	Finished    chan bool
	Log         chan string
	CurrentLine chan string
	TableName   chan string
	TableScheme chan string
	TableData   chan string
}

func isGzip(b *bufio.Reader) bool {
	if m, err := b.Peek(2); err == nil {
		return m[0] == 0x1f && m[1] == 0x8b
	}
	return false
}

var splitDumpCmd = &cobra.Command{
	Use:   "splitdump",
	Short: "Split MariaDB MySQL dump file",
	Long:  `Convert stdin stream to a directory and files similar to mydumper output`,
	Run: func(cmd *cobra.Command, args []string) {
		bus := SplitDumpChannelBus{
			Finished:    make(chan bool),
			TableData:   make(chan string),
			TableScheme: make(chan string),
			TableName:   make(chan string),
			CurrentLine: make(chan string),
		}

		fmt.Printf("Outputing all tables to %s\n", cliOutputDir)

		start := time.Now()
		if cliInputFile != "" {
			fmt.Printf("Begin processing %s\n", cliInputFile)
		}

		// create a pipeline of goroutines
		go SplitDumpLineReader(bus)
		go SplitDumpLineParser(bus, cliOutputDir)
		//, conf.Combine, conf.OutputPath, conf.SkipData, conf.SkipTable)

		// wait for the writer to finish.
		<-bus.Finished
		fmt.Printf("\n\n--finished in %s", time.Now().Sub(start))
		close(bus.Finished)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// On exit.
	},
}

func splitDumpOpenReader(f *os.File) *bufio.Reader {
	pageSize := os.Getpagesize() * 2
	buf := bufio.NewReaderSize(f, pageSize)

	if isGzip(buf) {
		gbuf, _ := gzip.NewReader(buf)
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
func SplitDumpLineReader(bus SplitDumpChannelBus) {

	r := splitDumpOpenReaderStdin()
	if cliInputFile != "" {
		file, err := splitDumpOpenFile(cliInputFile)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer file.Close()
		r = splitDumpOpenReader(file)
	}
	for line, err := r.ReadString('\n'); err == nil; line, err = r.ReadString('\n') {
		bus.CurrentLine <- line
	}

	close(bus.CurrentLine)
}

// LineParser reads the CurrentLine and figures out which channel to put it in.
func SplitDumpLineParser(bus SplitDumpChannelBus, outputDir string /*, combineFiles bool, outputDir string, skipData []string, skipTables []string*/) {
	onTableScheme, onTableData, pastHeader := false, false, false
	shardpad, schema := "", ""
	shard := 0
	binlogRegexMariaDB := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegexMariaDB := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)
	binlogRegexMySQL := regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	gtidRegexMySQL := regexp.MustCompile(`GTID_PURGED\s*=(\/\*!.+\*\/)?\s*'(.+)'`)
	headerMetaData := fmt.Sprintf("-- Generated with replication-manager on %s\n\n", time.Now())
	os.Mkdir(outputDir, os.ModePerm)
	// numTables := 0
	var f *os.File
	var tableFile *gzip.Writer
	var bgtid, bfile, bpos string

	streamSize := 0
	streamSizeMax := 1024 * 1024 * 1024
	for line := range bus.CurrentLine {
		//	fmt.Printf("%s %b %b %b", line, onTableScheme, onTableData)
		streamSize += len([]byte(line))

		if streamSize > streamSizeMax {
			shard++
			tableFile.Flush()
			tableFile.Close()
			f.Close()
			shardpad = fmt.Sprintf(".%05d", shard)
			tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table ", "", 1), "`", "", -1)) + shardpad
			fmt.Printf("Processing table data %s ", tableName)
			tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
			f, err := os.Create(tablePath)
			if err != nil {
				fmt.Printf("Error creating file %s %s\n", tablePath, err)
				bus.Finished <- true
				return
			}
			tableFile = gzip.NewWriter(f)
			streamSize = 0
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
				streamSize = 0
				tableFile.Close()
			}
		} else {
			if strings.HasPrefix(line, "USE ") {
				schema = strings.TrimSpace(strings.Replace(strings.Replace(strings.Replace(line, "USE ", "", 1), ";", "", -1), "`", "", -1))
			} else if strings.HasPrefix(line, "-- Table structure for table") {
				//case for empty table enter dummy data
				if onTableScheme && !onTableData {
					onTableScheme = false
					// add headers to each file unless we are combining all of them into 1 file.
					tableFile.Flush()
					tableFile.Close()
					f.Close()
				}
				onTableScheme, onTableData = true, false
				tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Table structure for table ", "", 1), "`", "", -1)) + "-schema"
				fmt.Printf("Processing table schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err := os.Create(tablePath)
				if err != nil {
					fmt.Printf("Error creating file %s %s\n", tablePath, err)
					bus.Finished <- true
					return
				}
				tableFile = gzip.NewWriter(f)
				pastHeader = true
			} else if strings.HasPrefix(line, "LOCK TABLES `") {
				onTableData, onTableScheme = true, false
			} else if strings.HasPrefix(line, "-- Dumping data for table") {
				// Case of data without table definition
				if !onTableScheme {
					tableFile.Flush()
					tableFile.Close()
					tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table", "", 1), "`", "", -1)) + "-schema"
					fmt.Printf("Processing table schema %s\n", tableName)
					tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
					f, err := os.Create(tablePath)
					if err != nil {
						fmt.Printf("Error creating file %s %s\n", tablePath, err)
						bus.Finished <- true
						return
					}
					tableFile = gzip.NewWriter(f)
					tableFile.Write([]byte("\n--\n" + line))
					pastHeader = true

				}
				tableFile.Flush()
				tableFile.Close()
				f.Close()
				shardpad = fmt.Sprintf(".%05d", shard)
				tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Dumping data for table ", "", 1), "`", "", -1)) + shardpad
				fmt.Printf("Processing table data %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err := os.Create(tablePath)
				if err != nil {
					fmt.Printf("Error creating file %s %s\n", tablePath, err)
					bus.Finished <- true
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
				//	onView = true
				tableFile.Flush()
				tableFile.Close()
				f.Close()
				tableName := schema + "." + strings.TrimSpace(strings.Replace(strings.Replace(line, "-- Final view structure for view ", "", 1), "`", "", -1)) + "-schema-view"
				fmt.Printf("Processing view schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err := os.Create(tablePath)
				if err != nil {
					fmt.Printf("Error creating file %s %s\n", tablePath, err)
					bus.Finished <- true
					return
				}
				tableFile = gzip.NewWriter(f)
				//-- Dumping routines
			} else if strings.HasPrefix(line, "INSTALL PLUGIN") || strings.HasPrefix(line, "CREATE USER") {
				onTableData = false
				onTableScheme = false
				tableFile.Flush()
				tableFile.Close()
				f.Close()
				tableName := "mysql.system-all"
				fmt.Printf("Processing view schema %s\n", tableName)
				tablePath := filepath.Join(outputDir, sanitizefilename.Sanitize(tableName)+".sql.gz")
				f, err := os.Create(tablePath)
				if err != nil {
					fmt.Printf("Error creating file %s %s\n", tablePath, err)
					bus.Finished <- true
					return
				}
				tableFile = gzip.NewWriter(f)
			}
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

		if pastHeader {
			if tableFile != nil {
				tableFile.Write([]byte(line))
			}
		}

	}
	tableFile.Flush()
	tableFile.Close()
	f.Close()
	tablePath := filepath.Join(outputDir, "metadata")
	f, err := os.Create(tablePath)
	if err != nil {
		fmt.Printf("Error creating file %s %s\n", tablePath, err)
		bus.Finished <- true
		return
	}
	line := fmt.Sprintf("[source]\n# Channel_Name = ''\nFile = %s\nPosition = %s\nExecuted_Gtid_Set = %s\n\n", bfile, bpos, bgtid)
	f.Write([]byte(line))
	f.Close()

	bus.Finished <- true
}
