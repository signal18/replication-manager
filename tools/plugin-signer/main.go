// plugin-signer — standalone Ed25519 key-management and signing tool for
// replication-manager external log plugins.
//
// This binary has zero replication-manager dependencies: it only uses the
// Go standard library so it compiles fast and can be built before (or
// independently of) any repman server binary.
//
// Commands:
//
//	plugin-signer keygen  --private-key <path> --public-key <path>
//	plugin-signer sign    --private-key <path> --sig-dir <path> <binary> [...]
//
// Security model:
//
//	The private key stays on the build/CI machine only.
//	The public key is deployed on every repman server and referenced via
//	'plugin-signing-public-key' in config.toml.
//	.sig sidecar files are distributed as part of the repman package/release.
package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const usage = `plugin-signer — Ed25519 plugin signing tool for replication-manager

Usage:
  plugin-signer keygen --private-key <path> --public-key <path>
  plugin-signer sign   --private-key <path> --sig-dir <path> <binary> [<binary>...]

Commands:
  keygen   Generate a new Ed25519 keypair.
  sign     Sign one or more plugin binaries; writes <name>.sig into --sig-dir.
`

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "keygen":
		runKeygen(os.Args[2:])
	case "sign":
		runSign(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

func runKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	privPath := fs.String("private-key", "", "Path to write the Ed25519 private key (build machine only)")
	pubPath := fs.String("public-key", "", "Path to write the Ed25519 public key (deploy to repman servers)")
	_ = fs.Parse(args)

	if *privPath == "" || *pubPath == "" {
		log.Fatal("keygen: --private-key and --public-key are required")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		log.Fatalf("keygen: failed to generate key: %v", err)
	}
	if err := writeKeyFile(*privPath, priv, 0600); err != nil {
		log.Fatalf("keygen: cannot write private key to %s: %v", *privPath, err)
	}
	if err := writeKeyFile(*pubPath, pub, 0644); err != nil {
		log.Fatalf("keygen: cannot write public key to %s: %v", *pubPath, err)
	}

	fmt.Printf("Private key → %s  (build machine only — never commit or deploy)\n", *privPath)
	fmt.Printf("Public  key → %s  (deploy to all repman servers)\n", *pubPath)
	fmt.Println()
	fmt.Println("Add to config.toml on every repman server:")
	fmt.Printf("  plugin-signing-public-key = %q\n", *pubPath)
}

func runSign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	privPath := fs.String("private-key", "", "Path to the Ed25519 private key")
	sigDir := fs.String("sig-dir", "", "Directory where .sig files are written")
	_ = fs.Parse(args)

	if *privPath == "" || *sigDir == "" {
		log.Fatal("sign: --private-key and --sig-dir are required")
	}
	binaries := fs.Args()
	if len(binaries) == 0 {
		log.Fatal("sign: at least one binary path is required")
	}

	privBytes, err := os.ReadFile(*privPath)
	if err != nil {
		log.Fatalf("sign: cannot read private key %s: %v", *privPath, err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		log.Fatalf("sign: invalid private key length %d (expected %d)",
			len(privBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)

	if err := os.MkdirAll(*sigDir, 0755); err != nil {
		log.Fatalf("sign: cannot create sig dir %s: %v", *sigDir, err)
	}

	for _, binPath := range binaries {
		data, err := os.ReadFile(binPath)
		if err != nil {
			log.Printf("sign: cannot read %s: %v — skipping", binPath, err)
			continue
		}
		sig := ed25519.Sign(priv, data)
		name := filepath.Base(binPath)
		sigFile := filepath.Join(*sigDir, name+".sig")
		if err := os.WriteFile(sigFile, sig, 0644); err != nil {
			log.Printf("sign: cannot write %s: %v — skipping", sigFile, err)
			continue
		}
		fmt.Printf("Signed: %-40s  →  %s\n", binPath, sigFile)
	}
	fmt.Println()
	fmt.Printf("Deploy the .sig files in %s as part of the repman package.\n", *sigDir)
	fmt.Println("Do NOT commit .sig files to the .pull repository.")
}

func writeKeyFile(path string, key []byte, perm os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("file already exists: %s (delete it first to regenerate)", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("cannot create parent directory: %w", err)
	}
	return os.WriteFile(path, key, perm)
}
