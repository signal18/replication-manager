// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// Bootstrap one-time password (GHSA-m3ph-v4wm-xg2g follow-up).
//
// The init container of a database or proxy service downloads its configuration tarball
// (root credentials in .cnf files, TLS keys) at every start. It used to log in with the
// cluster's admin account, whose password sat in the namespace secret `env` that every pod
// of the namespace can map. Each service now gets its own one-time password instead:
//
//   - a random value is written to the namespace secret under a per-service key
//     (bootstrap-otp-<service>), mapped into the init container only as
//     REPLICATION_MANAGER_OTP; repman keeps only its SHA-256, in the service's datadir;
//   - the bootstrap presents it in the X-Replication-Manager-Bootstrap-Otp header on that
//     service's config download, and nowhere else;
//   - repman accepts it once and rotates it: a new value is written to the secret for the
//     next start, the presented one stops working. When the new value cannot be delivered
//     the presented one stays valid, so a restart is never left without a credential.
//
// The service definition maps the key only once it has been delivered; until then the
// init container keeps the legacy admin login, so an existing service keeps starting.

// BootstrapOTPHeader carries the one-time password on the config download.
const BootstrapOTPHeader = "X-Replication-Manager-Bootstrap-Otp"

// bootstrapOTPEnv is the variable the init container receives the password in.
const bootstrapOTPEnv = "REPLICATION_MANAGER_OTP"

// bootstrapOTPKey is the per-service key of the namespace secret `env`.
func bootstrapOTPKey(serviceName string) string {
	parts := strings.Split(strings.TrimSpace(serviceName), "/")
	return "bootstrap-otp-" + parts[len(parts)-1]
}

func bootstrapOTPFile(datadir string) string {
	return filepath.Join(datadir, "bootstrap-otp.sha256")
}

func newBootstrapOTP() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashBootstrapOTP(otp string) string {
	sum := sha256.Sum256([]byte(otp))
	return hex.EncodeToString(sum[:])
}

// bootstrapOTPPush writes the value to the orchestrator secret; replaced in tests.
func (cluster *Cluster) bootstrapOTPPush(key, value string) error {
	if cluster.bootstrapOTPPusher != nil {
		return cluster.bootstrapOTPPusher(key, value)
	}
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return errors.New("bootstrap one-time password: OpenSVC only")
	}
	svc := cluster.OpenSVCConnect()
	return svc.CreateSecretKeyValue(cluster.Name, "env", key, value)
}

// BootstrapOTPDelivered: a one-time password has been delivered for the service whose
// datadir this is, so its definition may map it instead of the admin login.
func (cluster *Cluster) BootstrapOTPDelivered(datadir string) bool {
	if strings.TrimSpace(datadir) == "" {
		return false
	}
	_, err := os.Stat(bootstrapOTPFile(datadir))
	return err == nil
}

// EnsureBootstrapOTP delivers a first one-time password to a service that has none yet.
// Called before the service definition is generated for the orchestrator: the definition
// only maps the key once it exists, since a missing secret key fails the start.
func (cluster *Cluster) EnsureBootstrapOTP(serviceName, datadir string) error {
	cluster.bootstrapOTPMu.Lock()
	defer cluster.bootstrapOTPMu.Unlock()
	if cluster.BootstrapOTPDelivered(datadir) {
		return nil
	}
	return cluster.rotateBootstrapOTPLocked(serviceName, datadir)
}

// rotateBootstrapOTPLocked delivers a new value, then stores its hash; the hash is written
// only after the orchestrator holds the value, so the two never disagree.
func (cluster *Cluster) rotateBootstrapOTPLocked(serviceName, datadir string) error {
	if strings.TrimSpace(datadir) == "" || strings.TrimSpace(serviceName) == "" {
		return errors.New("bootstrap one-time password: no service name or datadir")
	}
	otp, err := newBootstrapOTP()
	if err != nil {
		return err
	}
	if err := cluster.bootstrapOTPPush(bootstrapOTPKey(serviceName), otp); err != nil {
		return err
	}
	if err := os.MkdirAll(datadir, 0700); err != nil {
		return err
	}
	tmp := bootstrapOTPFile(datadir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(hashBootstrapOTP(otp)), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, bootstrapOTPFile(datadir))
}

// ConsumeBootstrapOTP checks a presented one-time password against the service's stored
// hash (constant time) and, when it matches, rotates it for the next start. A rotation
// that cannot be delivered keeps the presented value valid and is reported, never fatal.
func (cluster *Cluster) ConsumeBootstrapOTP(serviceName, datadir, presented string) bool {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return false
	}
	cluster.bootstrapOTPMu.Lock()
	defer cluster.bootstrapOTPMu.Unlock()
	stored, err := os.ReadFile(bootstrapOTPFile(datadir))
	if err != nil {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(stored))), []byte(hashBootstrapOTP(presented))) != 1 {
		return false
	}
	if err := cluster.rotateBootstrapOTPLocked(serviceName, datadir); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Bootstrap one-time password of %s used but not rotated (%s): it stays valid until the next rotation", serviceName, err)
	}
	return true
}

// applyBootstrapOTPEnv maps the service's one-time password into its init container in
// place of the admin login.
func applyBootstrapOTPEnv(init map[string]string, serviceName string) {
	if init == nil {
		return
	}
	init["secrets_environment"] = bootstrapOTPEnv + "=env/" + bootstrapOTPKey(serviceName)
	init["configs_environment"] = "env/REPLICATION_MANAGER_URL"
}
