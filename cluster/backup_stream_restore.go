package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

const streamContainerMagicLen = 4

// isStreamContainerFile peeks at the first 4 bytes of path to detect the RMSC
// stream container magic. Returns (false, nil) for files that are too short or
// have a different prefix; returns an error only on I/O failures.
func isStreamContainerFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, streamContainerMagicLen)
	n, err := io.ReadFull(f, buf)
	if n < streamContainerMagicLen {
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil // file too short — not a stream container
		}
		return false, err // real I/O error — propagate to caller
	}
	return string(buf) == backupmgr.StreamContainerMagic, nil
}

// openStreamContainerEntry reads the preflight header from r, resolves the
// encryption key using sponsorCreds and apiCreds, and returns a FrameReader
// positioned at the start of the first (and only, for single-file mode) entry's
// frame payload.
//
// This is the core testable function shared by both the mysqldump and SST paths.
// The caller owns the FrameReader and is responsible for draining / discarding it.
func openStreamContainerEntry(ctx context.Context, r io.Reader, sponsorCreds, apiCreds string) (*backupmgr.FrameReader, *backupmgr.StreamPreflight, error) {
	preflight, err := backupmgr.ReadPreflight(r)
	if err != nil {
		return nil, nil, fmt.Errorf("stream container preflight: %w", err)
	}

	if preflight.Mode != backupmgr.StreamModeSingleFile {
		return nil, preflight, fmt.Errorf("stream container: expected single-file mode, got mode %d", preflight.Mode)
	}
	if len(preflight.Entries) == 0 {
		return nil, preflight, fmt.Errorf("stream container: no entries in single-file container")
	}

	rootSecret, _, err := backupmgr.ResolveStreamRootSecretForReference(sponsorCreds, apiCreds, preflight.KeyRef.KeyID)
	if err != nil {
		return nil, preflight, fmt.Errorf("stream container key resolution: %w: %w", backupmgr.ErrKeyResolutionFailed, err)
	}

	containerKey, err := backupmgr.DeriveStreamContainerKey(rootSecret, preflight.KeyRef.KeyCluster)
	if err != nil {
		return nil, preflight, fmt.Errorf("stream container key derivation: %w: %w", backupmgr.ErrKeyDerivationFailed, err)
	}

	entryKey, err := backupmgr.DeriveStreamEntryKey(containerKey, preflight.Entries[0].Path)
	if err != nil {
		return nil, preflight, fmt.Errorf("stream entry key derivation: %w: %w", backupmgr.ErrKeyDerivationFailed, err)
	}

	frameReader, err := backupmgr.NewFrameReader(ctx, r, entryKey, preflight.CipherSuite)
	if err != nil {
		return nil, preflight, fmt.Errorf("stream frame reader: %w", err)
	}

	return frameReader, preflight, nil
}

// openSingleFileStreamContainerReader opens path, reads the stream preflight,
// resolves keys from cluster credentials, and returns a decrypting ReadCloser
// that streams plaintext directly from the encrypted container.
//
// The caller must close the returned reader when done.
func (cluster *Cluster) openSingleFileStreamContainerReader(ctx context.Context, path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open stream container %s: %w", path, err)
	}

	sponsorCreds := cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials")
	apiCreds := cluster.Conf.GetDecryptedValue("api-credentials")

	frameReader, preflight, err := openStreamContainerEntry(ctx, f, sponsorCreds, apiCreds)
	if err != nil {
		_ = f.Close()
		cluster.logStreamLifecycleFailure(filepath.Base(path), err)
		return nil, err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo,
		"Stream container restore: opened %s (cipher=%s, entry=%s, version=%d)",
		path, preflight.CipherSuite, preflight.Entries[0].Path, preflight.Version)

	return &streamContainerReadCloser{FrameReader: frameReader, file: f}, nil
}

// logStreamLifecycleFailure emits a structured log event that categorises the
// error type so operators can distinguish preflight, key-derivation, and
// frame-authentication failures without parsing error strings.
func (cluster *Cluster) logStreamLifecycleFailure(basename string, err error) {
	switch {
	case errors.Is(err, backupmgr.ErrMalformedHeader),
		errors.Is(err, backupmgr.ErrUnsupportedVersion),
		errors.Is(err, backupmgr.ErrInvalidAlgorithm),
		errors.Is(err, backupmgr.ErrTruncatedHeader),
		errors.Is(err, backupmgr.ErrMissingKeyReference):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr,
			"Stream container preflight failed for %s: %v", basename, err)
	case errors.Is(err, backupmgr.ErrFrameAuthFailed):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr,
			"Stream container frame authentication failed for %s: %v", basename, err)
	case errors.Is(err, backupmgr.ErrKeyResolutionFailed):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr,
			"Stream container key resolution failed for %s: %v", basename, err)
	case errors.Is(err, backupmgr.ErrKeyDerivationFailed):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr,
			"Stream container key derivation failed for %s: %v", basename, err)
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr,
			"Stream container open failed for %s: %v", basename, err)
	}
}

// makeStreamContainerSSTOpener returns a SSTStreamOpener backed by the stream
// container at path. The opener opens the file, reads the preflight, and returns
// a decrypting FrameReader. The plaintext size is unknown (-1) since the stream
// container header does not store plaintext byte counts.
func (cluster *Cluster) makeStreamContainerSSTOpener(ctx context.Context, path string) SSTStreamOpener {
	return func() (io.ReadCloser, int64, error) {
		r, err := cluster.openSingleFileStreamContainerReader(ctx, path)
		if err != nil {
			return nil, 0, err
		}
		return r, -1, nil
	}
}

// streamContainerReadCloser wraps a FrameReader and closes the underlying file
// when Close is called.
type streamContainerReadCloser struct {
	*backupmgr.FrameReader
	file io.Closer
}

func (s *streamContainerReadCloser) Close() error {
	return s.file.Close()
}
