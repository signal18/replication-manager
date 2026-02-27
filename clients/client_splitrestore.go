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
	"unicode"

	"github.com/spf13/cobra"

	"github.com/signal18/replication-manager/utils/splitdump"
)

var (
	cliSplitRestoreDir           string
	cliSplitRestoreMysql         string
	cliSplitRestoreMysqlArg      string
	cliSplitRestoreParallel      int
	cliSplitRestoreUser          bool
	cliSplitRestoreDisableBinlog bool
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
	cmd.Flags().BoolVar(&cliSplitRestoreDisableBinlog, "disable-binlog", false, "Disable binary logging for splitdump restore")
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
		if splitdump.IsGtidSlavePosDataFile(path) {
			logger(splitdump.LogWarn, "Splitdump restore skipped mysql.gtid_slave_pos data file: %s", path)
			return nil
		}

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

		args, err := splitMysqlArgs(cliSplitRestoreMysqlArg)
		if err != nil {
			return fmt.Errorf("invalid mysql-args: %w", err)
		}
		cmd := exec.CommandContext(ctx, cliSplitRestoreMysql, args...)
		preamble := splitdump.RestorePreamble(path)
		// CLI requires an explicit flag to disable binlog per file.
		if cliSplitRestoreDisableBinlog {
			preamble = "SET sql_log_bin=0;" + preamble
		}
		if preamble != "" {
			reader = io.MultiReader(bytes.NewBufferString(preamble), reader)
		}
		cmd.Stdin = reader
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			errOutput := strings.TrimSpace(stderr.String())
			if errOutput == "" {
				errOutput = err.Error()
			}
			if splitdump.SchemaFromFilename(path) == "mysql" && splitdump.IsMissingTableError(errOutput) {
				logger(splitdump.LogWarn, "Splitdump restore skipped missing mysql table for %s: %s", path, errOutput)
				return nil
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

func splitMysqlArgs(input string) ([]string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, nil
	}

	var (
		args       []string
		current    strings.Builder
		quote      rune
		escaped    bool
		argStarted bool
	)

	flush := func() {
		args = append(args, current.String())
		current.Reset()
		argStarted = false
	}

	runes := []rune(trimmed)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			current.WriteRune(r)
			escaped = false
			argStarted = true
			continue
		}

		if r == '\\' {
			switch quote {
			case '\'':
				current.WriteRune(r)
				argStarted = true
				continue
			case 0:
				if i+1 < len(runes) {
					next := runes[i+1]
					if unicode.IsSpace(next) || next == '\'' || next == '"' {
						escaped = true
						argStarted = true
						continue
					}
				}
				current.WriteRune(r)
				argStarted = true
				continue
			default:
				escaped = true
				argStarted = true
				continue
			}
		}

		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
				argStarted = true
				continue
			}
			if quote == r {
				quote = 0
				continue
			}
			current.WriteRune(r)
			argStarted = true
			continue
		}

		if quote == 0 && unicode.IsSpace(r) {
			if argStarted {
				flush()
			}
			continue
		}

		current.WriteRune(r)
		argStarted = true
	}

	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unmatched quote")
	}
	if argStarted {
		flush()
	}

	return args, nil
}
