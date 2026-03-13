//go:build clients
// +build clients

package clients

import (
	"bytes"
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

	encodedCiphertext, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	outputData, err := decryptWithPassphraseOpenSSL(encodedCiphertext, password)
	if err != nil {
		key := rmcrypto.GetSHA256Hash(password)
		iv := rmcrypto.GetMD5Hash(password)
		legacyData, legacyErr := decryptWithLegacyKeyIVOpenSSL(encodedCiphertext, key, iv)
		if legacyErr != nil {
			return fmt.Errorf("failed to decrypt backup (passphrase mode): %s; legacy mode: %s", err.Error(), legacyErr.Error())
		}
		outputData = legacyData
	}

	if len(outputData) == 0 {
		return fmt.Errorf("decryption produced empty output")
	}
	if err := os.WriteFile(output, outputData, 0600); err != nil {
		return fmt.Errorf("failed to write decrypted output: %w", err)
	}

	fmt.Printf("Decrypted backup written to %s\n", output)
	return nil
}

func decryptWithPassphraseOpenSSL(encodedCiphertext []byte, password string) ([]byte, error) {
	cmd := exec.Command("openssl", "enc", "-d", "-aes-256-cbc", "-a", "-pass", "pass:"+password)
	cmd.Stdin = bytes.NewReader(encodedCiphertext)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	return out.Bytes(), nil
}

func decryptWithLegacyKeyIVOpenSSL(encodedCiphertext []byte, key, iv string) ([]byte, error) {
	cmd := exec.Command("openssl", "aes-256-cbc", "-d", "-a", "-nosalt", "-K", key, "-iv", iv)
	cmd.Stdin = bytes.NewReader(encodedCiphertext)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	return out.Bytes(), nil
}

func defaultDecryptedBackupPath(input string) string {
	trimmed := strings.TrimSpace(input)
	if strings.HasSuffix(strings.ToLower(trimmed), ".enc") {
		return strings.TrimSuffix(trimmed, filepath.Ext(trimmed))
	}
	return trimmed + ".dec"
}
