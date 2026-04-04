// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	pluginPrivateKey string
	pluginPublicKey  string
	pluginSigDir     string
	pluginOverwrite  bool
)

func init() {
	rootCmd.AddCommand(pluginKeygenCmd)
	rootCmd.AddCommand(pluginSignCmd)

	pluginKeygenCmd.Flags().StringVar(&pluginPrivateKey, "plugin-private-key", "", "Output path for the Ed25519 private key (required)")
	pluginKeygenCmd.Flags().StringVar(&pluginPublicKey, "plugin-public-key", "", "Output path for the Ed25519 public key (required)")
	pluginKeygenCmd.Flags().BoolVar(&pluginOverwrite, "overwrite", false, "Overwrite existing key files")
	pluginKeygenCmd.MarkFlagRequired("plugin-private-key")
	pluginKeygenCmd.MarkFlagRequired("plugin-public-key")

	pluginSignCmd.Flags().StringVar(&pluginPrivateKey, "plugin-private-key", "", "Path to the Ed25519 private key (required)")
	pluginSignCmd.Flags().StringVar(&pluginSigDir, "sig-output-dir", "share/plugins", "Directory where .sig files are written")
	pluginSignCmd.MarkFlagRequired("plugin-private-key")
}

var pluginKeygenCmd = &cobra.Command{
	Use:   "plugin-keygen",
	Short: "Generate an Ed25519 keypair for plugin signing",
	Long: `Generates a new Ed25519 keypair used to sign and verify external log plugin binaries.
The private key is written to --plugin-private-key (mode 0600).
The public key is written to --plugin-public-key (mode 0644).
Use --overwrite to replace existing key files.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runPluginKeygen(pluginPrivateKey, pluginPublicKey, pluginOverwrite); err != nil {
			log.Fatalln(err)
		}
	},
}

var pluginSignCmd = &cobra.Command{
	Use:   "plugin-sign [plugin-binary ...]",
	Short: "Sign one or more plugin binaries with the Ed25519 private key",
	Long: `Signs each plugin binary using the Ed25519 private key.
The SHA-256 hash of the binary content is signed and the 64-byte signature is written
to <sig-output-dir>/<binary-name>.sig.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		privKey, err := readEd25519PrivKey(pluginPrivateKey)
		if err != nil {
			log.Fatalf("read private key: %v", err)
		}
		if err := os.MkdirAll(pluginSigDir, 0755); err != nil {
			log.Fatalf("create sig dir: %v", err)
		}
		for _, binPath := range args {
			if err := signPlugin(privKey, binPath, pluginSigDir); err != nil {
				log.Fatalf("sign %s: %v", binPath, err)
			}
			fmt.Printf("signed %s → %s/%s.sig\n", binPath, pluginSigDir, filepath.Base(binPath))
		}
	},
}

// runPluginKeygen generates an Ed25519 keypair and writes it to disk.
func runPluginKeygen(privPath, pubPath string, overwrite bool) error {
	if !overwrite {
		for _, p := range []string{privPath, pubPath} {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("key file already exists: %s (use --overwrite to replace)", p)
			}
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}

	if err := writeFile(privPath, priv, 0600, overwrite); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := writeFile(pubPath, pub, 0644, overwrite); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}

	fmt.Printf("private key → %s\n", privPath)
	fmt.Printf("public key  → %s\n", pubPath)
	return nil
}

// signPlugin signs the binary at binPath using privKey and writes the .sig file.
func signPlugin(privKey ed25519.PrivateKey, binPath, sigDir string) error {
	content, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	hash := sha256.Sum256(content)
	sig := ed25519.Sign(privKey, hash[:])

	sigPath := filepath.Join(sigDir, filepath.Base(binPath)+".sig")
	return os.WriteFile(sigPath, sig, 0644)
}

// readEd25519PrivKey reads a raw 64-byte Ed25519 private key from disk.
func readEd25519PrivKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: got %d bytes, want %d", len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}

// VerifyPluginSignature verifies the Ed25519 signature of a plugin binary.
// pubKeyPath is the path to the raw 32-byte public key file.
// sigPath is the path to the raw 64-byte signature file.
// binPath is the plugin binary to verify.
func VerifyPluginSignature(pubKeyPath, sigPath, binPath string) error {
	pubBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("read public key: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: got %d bytes, want %d", len(pubBytes), ed25519.PublicKeySize)
	}

	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: got %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}

	content, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}

	hash := sha256.Sum256(content)
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), hash[:], sig) {
		return errors.New("signature verification failed")
	}
	return nil
}

// writeFile writes data to path, optionally allowing overwrite.
func writeFile(path string, data []byte, mode os.FileMode, overwrite bool) error {
	flag := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flag |= os.O_TRUNC
	} else {
		flag |= os.O_EXCL
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, flag, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
