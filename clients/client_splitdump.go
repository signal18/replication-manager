//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/spf13/cobra"
)

var splitDumpCmd = &cobra.Command{
	Use:   "splitdump",
	Short: "Split MariaDB MySQL dump file",
	Long:  `Convert stdin stream to a directory and files similar to mydumper output`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runSplitdump(cliInputFile, cliOutputDir); err != nil {
			fmt.Println(err)
		}
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// On exit.
	},
}

func runSplitdump(inputFile, outputDir string) error {
	bus := splitdump.NewSplitDumpChannelBus()

	fmt.Printf("Outputing all tables to %s\n", outputDir)

	start := time.Now()
	if inputFile != "" {
		fmt.Printf("Begin processing %s\n", inputFile)
	}

	// create a pipeline of goroutines
	go splitdump.SplitDumpLineReader(bus, inputFile)
	go splitdump.SplitDumpLineParser(bus, outputDir)
	//, conf.Combine, conf.OutputPath, conf.SkipData, conf.SkipTable)

	// wait for the writer to finish.
	<-bus.Finished
	fmt.Printf("\n\n--finished in %s", time.Since(start))
	close(bus.Finished)
	return nil
}
