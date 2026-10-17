// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/PlakarKorp/kloset/caching"
	"github.com/PlakarKorp/kloset/caching/pebble"
	"github.com/PlakarKorp/kloset/compression"
	"github.com/PlakarKorp/kloset/connectors"
	"github.com/PlakarKorp/kloset/connectors/storage"
	"github.com/PlakarKorp/kloset/encryption"
	"github.com/PlakarKorp/kloset/hashing"
	"github.com/PlakarKorp/kloset/kcontext"
	"github.com/PlakarKorp/kloset/logging"
	"github.com/PlakarKorp/kloset/repository"
	"github.com/PlakarKorp/kloset/resources"
	"github.com/PlakarKorp/kloset/versioning"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"

	// the fs storage, importer and exporter of the plakar engine
	_ "github.com/PlakarKorp/integrations/fs/exporter"
	_ "github.com/PlakarKorp/integrations/fs/importer"
	_ "github.com/PlakarKorp/integrations/fs/storage"
)

// logf writes a progress line on stderr: stdout carries only the response.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "archiveplugin-plakar: "+format+"\n", args...)
}

// secrets are an archive's secrets, read from the environment, never logged.
type secrets struct {
	password     string
	accessKey    string
	secretKey    string
	sessionToken string
}

// mainSecrets are the secrets of Request.Store.
func mainSecrets() secrets {
	return secrets{
		password:     os.Getenv(wire.SecretEnv(setPassword)),
		accessKey:    os.Getenv(wire.EnvS3AccessKey),
		secretKey:    os.Getenv(wire.EnvS3SecretKey),
		sessionToken: os.Getenv(wire.EnvS3SessionToken),
	}
}

// targetSecrets are the secrets of Request.SyncTo: the TARGET_ ones, the source's S3 keys
// when the target has none of its own.
func targetSecrets() secrets {
	s := secrets{
		password:     os.Getenv(wire.SecretEnv(secTargetPassword)),
		accessKey:    os.Getenv(wire.SecretEnv(secTargetS3AccessKey)),
		secretKey:    os.Getenv(wire.SecretEnv(secTargetS3SecretKey)),
		sessionToken: os.Getenv(wire.SecretEnv(secTargetS3SessionTok)),
	}
	if s.accessKey == "" {
		m := mainSecrets()
		s.accessKey, s.secretKey, s.sessionToken = m.accessKey, m.secretKey, m.sessionToken
	}
	return s
}

// session is the engine context of one run: its kloset context and local cache.
type session struct {
	ctx      *kcontext.KContext
	cacheDir string
	release  func()
}

// newSession sets up the kloset context of a run on store st, host the snapshots' host.
func newSession(st wire.Store, host string) (*session, error) {
	cacheDir, release, err := lockCacheDir(st)
	if err != nil {
		return nil, err
	}
	ctx := kcontext.NewKContext()
	ctx.Stdin = bytes.NewReader(nil)
	ctx.Stdout = os.Stderr
	ctx.Stderr = os.Stderr
	ctx.Client = "replication-manager-archiveplugin-plakar/1"
	ctx.CWD, _ = os.Getwd()
	if ctx.CWD == "" {
		ctx.CWD = "/"
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	ctx.Hostname = host
	ctx.Username = os.Getenv("USER")
	ctx.OperatingSystem = runtime.GOOS
	ctx.Architecture = runtime.GOARCH
	ctx.ProcessID = os.Getpid()
	ctx.MaxConcurrency = runtime.NumCPU()
	ctx.CacheDir = cacheDir
	ctx.SetCache(caching.NewManager(pebble.Constructor(cacheDir)))
	logger := logging.NewLogger(os.Stderr, os.Stderr)
	logger.EnableInfo()
	ctx.SetLogger(logger)
	return &session{ctx: ctx, cacheDir: cacheDir, release: release}, nil
}

func (s *session) close() {
	if err := s.ctx.GetCache().Close(); err != nil {
		logf("close cache: %s", err)
	}
	s.ctx.Close()
	s.release()
}

// connectorOptions are the importer and exporter options of the session.
func (s *session) connectorOptions() *connectors.Options {
	return &connectors.Options{
		Hostname:        s.ctx.Hostname,
		OperatingSystem: s.ctx.OperatingSystem,
		Architecture:    s.ctx.Architecture,
		CWD:             s.ctx.CWD,
		MaxConcurrency:  s.ctx.MaxConcurrency,
		Stdin:           s.ctx.Stdin,
		Stdout:          os.Stderr,
		Stderr:          os.Stderr,
	}
}

// lockCacheDir picks the cache folder of store st (the cache-dir setting, or plakar-<store
// hash> under the user cache dir or $TMPDIR) and takes its lock: the engine's caches are not
// shared between processes, so a run that finds the cache in use by another run on the same
// archive works in a fresh temporary cache, removed at its end (the state is rebuilt from
// the archive, slower but correct).
func lockCacheDir(st wire.Store) (string, func(), error) {
	dir := st.Settings[setCacheDir]
	if dir == "" {
		sum := sha256.Sum256([]byte(st.Plugin + "\x00" + storeIdentity(st)))
		name := "plakar-" + hex.EncodeToString(sum[:8])
		base, err := os.UserCacheDir()
		if err != nil || os.MkdirAll(filepath.Join(base, name), 0700) != nil {
			base = os.TempDir()
		}
		dir = filepath.Join(base, name)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, fmt.Errorf("cache dir: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("cache dir lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		tmp, terr := os.MkdirTemp("", "plakar-cache-*")
		if terr != nil {
			return "", nil, fmt.Errorf("cache dir %s in use and no temporary one: %w", dir, terr)
		}
		logf("cache %s in use by another run, using a temporary cache", dir)
		return tmp, func() { os.RemoveAll(tmp) }, nil
	}
	return dir, func() {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}

// storeIdentity names the archive for the cache hash (no secret in it).
func storeIdentity(st wire.Store) string {
	switch storeKind(st.Plugin) {
	case "local":
		return st.Settings[setLocation]
	case "s3":
		return st.Endpoint + "/" + st.Settings[setBucket] + "/" + strings.Trim(st.Settings[setPrefix], "/")
	}
	return st.Name
}

// storeConfig is the kloset storage configuration of st and its location as shown to
// replication-manager (never with a secret).
func storeConfig(st wire.Store, sec secrets) (map[string]string, string, error) {
	switch storeKind(st.Plugin) {
	case "local":
		loc := st.Settings[setLocation]
		if loc == "" {
			return nil, "", errors.New("local archive: no location")
		}
		abs, err := filepath.Abs(loc)
		if err != nil {
			return nil, "", err
		}
		abs = filepath.Clean(abs)
		if abs == "/" {
			return nil, "", errors.New("local archive: refusing / as location")
		}
		return map[string]string{"location": "fs://" + abs}, abs, nil
	case "s3":
		bucket := strings.Trim(st.Settings[setBucket], "/")
		if bucket == "" {
			return nil, "", errors.New("s3 archive: no bucket")
		}
		endpoint := strings.TrimSpace(st.Endpoint)
		useTLS := true
		switch {
		case strings.HasPrefix(endpoint, "https://"):
			endpoint = strings.TrimPrefix(endpoint, "https://")
		case strings.HasPrefix(endpoint, "http://"):
			endpoint = strings.TrimPrefix(endpoint, "http://")
			useTLS = false
		}
		endpoint = strings.TrimSuffix(endpoint, "/")
		if endpoint == "" {
			return nil, "", errors.New("s3 archive: no endpoint")
		}
		if v := st.Settings[setUseTLS]; v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, "", fmt.Errorf("s3 archive: %s: %w", setUseTLS, err)
			}
			useTLS = b
		}
		loc := endpoint + "/" + bucket
		if prefix := strings.Trim(st.Settings[setPrefix], "/"); prefix != "" {
			loc += "/" + prefix
		}
		return map[string]string{
			"location":         "s3://" + loc,
			s3ConfAccessKey:    sec.accessKey,
			s3ConfSecretKey:    sec.secretKey,
			s3ConfSessionToken: sec.sessionToken,
			s3ConfUseTLS:       strconv.FormatBool(useTLS),
			s3ConfRegion:       st.Region,
		}, "s3://" + loc, nil
	}
	return nil, "", fmt.Errorf("unknown archive plugin %q", st.Plugin)
}

// archive is an opened plakar repository.
type archive struct {
	kind     string
	location string
	store    storage.Store
	repo     *repository.Repository
	created  bool
	sess     *session
}

func (a *archive) close() {
	if a.repo != nil {
		a.repo.Close()
	}
	if a.store != nil {
		if err := a.store.Close(a.sess.ctx); err != nil {
			logf("close store: %s", err)
		}
	}
}

// openStore opens the storage of st and reads its configuration: fs.ErrNotExist when the
// archive does not exist. The store is closed on error.
func openStore(sess *session, st wire.Store, sec secrets) (storage.Store, string, []byte, error) {
	conf, loc, err := storeConfig(st, sec)
	if err != nil {
		return nil, "", nil, err
	}
	store, err := storage.New(sess.ctx, conf)
	if err != nil {
		return nil, loc, nil, fmt.Errorf("archive %s: %w", loc, err)
	}
	cfg, err := store.Open(sess.ctx)
	if err != nil {
		store.Close(sess.ctx)
		return nil, loc, nil, fmt.Errorf("archive %s: %w", loc, err)
	}
	return store, loc, cfg, nil
}

// openArchive opens the archive st with its passphrase, creating it first when missing
// and create is set, and rebuilds the local state of the repository from the archive.
func openArchive(sess *session, st wire.Store, sec secrets, create bool) (*archive, error) {
	a := &archive{kind: storeKind(st.Plugin), sess: sess}
	store, loc, cfg, err := openStore(sess, st, sec)
	a.location = loc
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if !create {
			return nil, fmt.Errorf("archive %s does not exist", loc)
		}
		if err := createArchive(sess, st, sec); err != nil {
			return nil, fmt.Errorf("create archive %s: %w", loc, err)
		}
		logf("created archive %s", loc)
		a.created = true
		if store, _, cfg, err = openStore(sess, st, sec); err != nil {
			return nil, err
		}
	}
	a.store = store

	key, err := unlock(cfg, sec.password, loc)
	if err != nil {
		a.close()
		return nil, err
	}
	repo, err := repository.New(sess.ctx, key, store, cfg)
	if err != nil {
		a.close()
		return nil, fmt.Errorf("archive %s: %w", loc, err)
	}
	a.repo = repo
	return a, nil
}

// unlock checks the archive's configuration and derives its key from the passphrase: a
// wrong passphrase fails on the canary, before anything is read.
func unlock(cfg []byte, password, loc string) ([]byte, error) {
	repoConfig, err := storage.NewConfigurationFromWrappedBytes(cfg)
	if err != nil {
		return nil, fmt.Errorf("archive %s: not a plakar archive configuration: %w", loc, err)
	}
	if repoConfig.Version != versioning.FromString(storage.VERSION) {
		return nil, fmt.Errorf("archive %s: incompatible version %s, this plugin speaks %s", loc, repoConfig.Version, storage.VERSION)
	}
	if repoConfig.Encryption == nil {
		if password != "" {
			return nil, fmt.Errorf("archive %s is not encrypted but a passphrase is set: refusing an archive that is not the expected one", loc)
		}
		return nil, nil
	}
	if password == "" {
		return nil, fmt.Errorf("archive %s is encrypted and no passphrase is set (%s)", loc, wire.SecretEnv(setPassword))
	}
	key, err := encryption.DeriveKey(repoConfig.Encryption.KDFParams, []byte(password))
	if err != nil {
		return nil, err
	}
	if !encryption.VerifyCanary(repoConfig.Encryption, key) {
		return nil, fmt.Errorf("archive %s: wrong passphrase", loc)
	}
	return key, nil
}

// createArchive creates the archive st as plakar create does: the default configuration,
// encrypted with a key derived from the passphrase (its canary to verify it), compressed
// unless the compression setting is off, serialized under the key's MAC.
func createArchive(sess *session, st wire.Store, sec secrets) error {
	if sec.password == "" {
		return fmt.Errorf("no passphrase (%s): an archive is always encrypted", wire.SecretEnv(setPassword))
	}
	conf, _, err := storeConfig(st, sec)
	if err != nil {
		return err
	}
	store, err := storage.New(sess.ctx, conf)
	if err != nil {
		return err
	}
	defer store.Close(sess.ctx)

	config := storage.NewConfiguration()
	config.Compression = compression.NewDefaultConfiguration()
	if v := st.Settings[setCompression]; v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", setCompression, err)
		}
		if !on {
			config.Compression = nil
		}
	}
	hashingConfig, err := hashing.LookupDefaultConfiguration(hashing.DEFAULT_HASHING_ALGORITHM)
	if err != nil {
		return err
	}
	config.Hashing = *hashingConfig

	key, err := encryption.DeriveKey(config.Encryption.KDFParams, []byte(sec.password))
	if err != nil {
		return err
	}
	canary, err := encryption.DeriveCanary(config.Encryption, key)
	if err != nil {
		return err
	}
	config.Encryption.Canary = canary
	hasher := hashing.GetMACHasher(storage.DEFAULT_HASHING_ALGORITHM, key)

	serialized, err := config.ToBytes()
	if err != nil {
		return err
	}
	rd, err := storage.Serialize(hasher, resources.RT_CONFIG, versioning.GetCurrentVersion(resources.RT_CONFIG), bytes.NewReader(serialized))
	if err != nil {
		return err
	}
	wrapped, err := io.ReadAll(rd)
	if err != nil {
		return err
	}
	return store.Create(sess.ctx, wrapped)
}
