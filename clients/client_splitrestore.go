//go:build clients
// +build clients

package clients

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/signal18/replication-manager/utils/splitdump"
)

var (
	cliSplitRestoreDir      string
	cliSplitRestoreMysql    string
	cliSplitRestoreMysqlArg string
	cliSplitRestoreParallel int
	cliSplitRestoreUser     bool
)

var splitRestoreCmd = &cobra.Command{
	Use:   "splitrestore",
	Short: "Restore splitdump directory",
	Long:  "Restore a splitdump directory by streaming each file into the mysql client",
	Run: func(cmd *cobra.Command, args []string) {
		if err := runSplitrestore(cmd.Context()); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

func initSplitRestoreFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliSplitRestoreDir, "input-dir", "", "Splitdump directory to restore")
	cmd.Flags().StringVar(&cliSplitRestoreMysql, "mysql", "mysql", "Path to mysql client binary")
	cmd.Flags().StringVar(&cliSplitRestoreMysqlArg, "mysql-args", "--force --batch", "Extra mysql client arguments")
	cmd.Flags().IntVar(&cliSplitRestoreParallel, "parallel", 1, "Parallel workers for splitdump restore")
	cmd.Flags().BoolVar(&cliSplitRestoreUser, "restore-user", true, "Restore mysql.system-all file when present")
}

func runSplitrestore(ctx context.Context) error {
	if strings.TrimSpace(cliSplitRestoreDir) == "" {
		return fmt.Errorf("input-dir is required")
	}

	start := time.Now()
	logger := func(level, format string, args ...any) {
		fmt.Printf("["+level+"] "+format+"\n", args...)
	}

	mysqlPath, err := exec.LookPath(cliSplitRestoreMysql)
	if err != nil {
		return fmt.Errorf("mysql client not found at %s: %w", cliSplitRestoreMysql, err)
	}
	cliSplitRestoreMysql = mysqlPath

	restoreFile := func(ctx context.Context, path string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		var reader io.Reader = file
		if strings.HasSuffix(strings.ToLower(path), ".gz") {
			gzReader, err := gzip.NewReader(file)
			if err != nil {
				return err
			}
			defer gzReader.Close()
			reader = gzReader
		}

		args := strings.Fields(cliSplitRestoreMysqlArg)
		cmd := exec.CommandContext(ctx, cliSplitRestoreMysql, args...)
		cmd.Stdin = reader
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			errOutput := strings.TrimSpace(stderr.String())
			if errOutput == "" {
				errOutput = err.Error()
			}
			return fmt.Errorf("mysql restore failed for %s: %s", path, errOutput)
		}
		return nil
	}

	if err := splitdump.Restore(cliSplitRestoreDir, splitdump.RestoreOptions{
		Parallel:               cliSplitRestoreParallel,
		RestoreUser:            cliSplitRestoreUser,
		Logger:                 logger,
		Context:                ctx,
		RestoreFileWithContext: restoreFile,
	}); err != nil {
		return err
	}

	fmt.Printf("\n\n--finished in %s", time.Since(start))
	return nil
}
