//go:build clients
// +build clients

package clients

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	rmcrypto "github.com/signal18/replication-manager/utils/crypto"
	"github.com/spf13/cobra"
)

var (
	cliDecryptBackupInput    string
	cliDecryptBackupOutput   string
	cliDecryptBackupPassword string
)

var decryptBackupCmd = &cobra.Command{
	Use:   "decrypt-backup",
	Short: "Decrypt an encrypted backup file",
	Long:  "Decrypt an AES-256-CBC backup file produced by replication-manager backup encryption",
	Run: func(cmd *cobra.Command, args []string) {
		if err := runDecryptBackup(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

func initDecryptBackupFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliDecryptBackupInput, "input", "", "Encrypted backup file path (.enc)")
	cmd.Flags().StringVar(&cliDecryptBackupOutput, "output", "", "Decrypted output file path (default: input without .enc)")
	cmd.Flags().StringVar(&cliDecryptBackupPassword, "password", "", "Password used for backup encryption (leave empty to prompt)")
}

func runDecryptBackup() error {
	input := strings.TrimSpace(cliDecryptBackupInput)
	if input == "" {
		return fmt.Errorf("input is required")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input file not found: %w", err)
	}

	output := strings.TrimSpace(cliDecryptBackupOutput)
	if output == "" {
		output = defaultDecryptedBackupPath(input)
	}
	if output == input {
		return fmt.Errorf("output must be different from input")
	}

	password := strings.TrimSpace(cliDecryptBackupPassword)
	if password == "" {
		password = cliGetpasswd()
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	passphraseTmp := output + ".tmp.passphrase"
	legacyTmp := output + ".tmp.legacy"

	passErr := decryptWithPassphraseOpenSSL(input, passphraseTmp, password)
	if passErr == nil {
		if err := ensureNonEmptyFile(passphraseTmp); err != nil {
			_ = os.Remove(passphraseTmp)
			return err
		}
		if err := os.Rename(passphraseTmp, output); err != nil {
			_ = os.Remove(passphraseTmp)
			return fmt.Errorf("failed to write decrypted output: %w", err)
		}
		_ = os.Remove(legacyTmp)

		fmt.Printf("Decrypted backup written to %s\n", output)
		return nil
	}
	_ = os.Remove(passphraseTmp)

	key := rmcrypto.GetSHA256Hash(password)
	iv := rmcrypto.GetMD5Hash(password)
	legacyErr := decryptWithLegacyKeyIVOpenSSL(input, legacyTmp, key, iv)
	if legacyErr != nil {
		_ = os.Remove(legacyTmp)
		return fmt.Errorf("failed to decrypt backup (passphrase mode): %s; legacy mode: %s", passErr.Error(), legacyErr.Error())
	}

	if err := ensureNonEmptyFile(legacyTmp); err != nil {
		_ = os.Remove(legacyTmp)
		return err
	}
	if err := os.Rename(legacyTmp, output); err != nil {
		_ = os.Remove(legacyTmp)
		return fmt.Errorf("failed to write decrypted output: %w", err)
	}

	fmt.Printf("Decrypted backup written to %s\n", output)
	return nil
}

func decryptWithPassphraseOpenSSL(inputPath, outputPath, password string) error {
	args := []string{"enc", "-d", "-aes-256-cbc", "-a", "-pass", "pass:" + password}
	return runOpenSSLStream(inputPath, outputPath, args...)
}

func decryptWithLegacyKeyIVOpenSSL(inputPath, outputPath, key, iv string) error {
	args := []string{"aes-256-cbc", "-d", "-a", "-nosalt", "-K", key, "-iv", iv}
	return runOpenSSLStream(inputPath, outputPath, args...)
}

func runOpenSSLStream(inputPath, outputPath string, args ...string) error {
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer input.Close()

	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer output.Close()

	cmd := exec.Command("openssl", args...)
	cmd.Stdin = input
	cmd.Stdout = output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if errMsg == "" {
			if exitCode >= 0 {
				return fmt.Errorf("openssl failed (exit=%d)", exitCode)
			}
			return err
		}
		if exitCode >= 0 {
			return fmt.Errorf("openssl failed (exit=%d): %s", exitCode, errMsg)
		}
		return fmt.Errorf("openssl failed: %s", errMsg)
	}

	if err := output.Sync(); err != nil {
		return err
	}
	return nil
}

func ensureNonEmptyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat decrypted output: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("decryption produced empty output")
	}
	return nil
}

func defaultDecryptedBackupPath(input string) string {
	trimmed := strings.TrimSpace(input)
	if strings.HasSuffix(strings.ToLower(trimmed), ".enc") {
		return strings.TrimSuffix(trimmed, filepath.Ext(trimmed))
	}
	return trimmed + ".dec"
}
