package pbscommon

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// 8-byte magic prefixing every PBS dynamic-index (.didx) file.
var didxMagic = []byte{28, 145, 78, 165, 25, 186, 179, 205}

const (
	didxHeaderSize = 4096
	didxEntrySize  = 40 // 8 bytes cumulative end-offset (uint64 LE) + 32 bytes SHA-256 digest
)

// ParsePreviousDIDXChunkDigests extracts the hex chunk digests from a previously
// downloaded .didx body so a caller can seed a known-chunks set for dedup. It is
// deliberately defensive: a body that is missing, shorter than the 4096-byte
// header, has the wrong magic, or whose entry section is not a whole number of
// 40-byte records returns nil instead of panicking — the caller then simply
// re-uploads all chunks (correct, just without dedup). This replaces the ad-hoc
// previousDidx[4096:] slicing that panicked on a truncated or odd-length transfer
// (and on a sub-8-byte 404 body).
func ParsePreviousDIDXChunkDigests(previousDidx []byte) []string {
	if len(previousDidx) < didxHeaderSize || !bytes.HasPrefix(previousDidx, didxMagic) {
		return nil
	}
	entries := previousDidx[didxHeaderSize:]
	if len(entries)%didxEntrySize != 0 {
		return nil
	}
	digests := make([]string, 0, len(entries)/didxEntrySize)
	for base := 0; base < len(entries); base += didxEntrySize {
		digests = append(digests, hex.EncodeToString(entries[base+8:base+40]))
	}
	return digests
}

// didxIndex is the parsed contents of a .didx dynamic-index file: the ordered
// list of chunk digests and their cumulative end-offsets in the reconstructed
// stream. offsets[i] is the byte offset one past the end of chunk i.
type didxIndex struct {
	offsets []uint64
	digests []string
	total   uint64
}

// downloadDIDXIndex fetches and parses a .didx index. The .didx file is NOT the
// archive itself — it is an index of cumulative end-offsets and chunk digests.
func (pbs *PBSClient) downloadDIDXIndex(archiveName string) (*didxIndex, error) {
	indexBytes, err := pbs.DownloadToBytes(archiveName)
	if err != nil {
		return nil, fmt.Errorf("download index %q: %w", archiveName, err)
	}
	if len(indexBytes) < didxHeaderSize {
		return nil, fmt.Errorf("index %q: short read (%d bytes, need at least %d)",
			archiveName, len(indexBytes), didxHeaderSize)
	}
	if !bytes.HasPrefix(indexBytes, didxMagic) {
		return nil, fmt.Errorf("index %q: invalid DIDX magic", archiveName)
	}

	entries := indexBytes[didxHeaderSize:]
	if len(entries)%didxEntrySize != 0 {
		return nil, fmt.Errorf("index %q: entries section length %d is not a multiple of %d",
			archiveName, len(entries), didxEntrySize)
	}
	chunkCount := len(entries) / didxEntrySize

	idx := &didxIndex{
		offsets: make([]uint64, chunkCount),
		digests: make([]string, chunkCount),
	}
	for i := 0; i < chunkCount; i++ {
		base := i * didxEntrySize
		idx.offsets[i] = binary.LittleEndian.Uint64(entries[base : base+8])
		idx.digests[i] = hex.EncodeToString(entries[base+8 : base+40])
		// Offsets are cumulative END offsets and must be strictly ascending (each
		// chunk has non-zero length): chunkIndexAt (sort.Search) and chunkRange
		// rely on it. A corrupt or hostile index with a non-monotonic offset would
		// otherwise yield an out-of-range chunk index and panic on chunk[pos-start:]
		// in ReadAt (uint64 underflow), reachable from listing/search.
		prev := uint64(0)
		if i > 0 {
			prev = idx.offsets[i-1]
		}
		if idx.offsets[i] <= prev {
			return nil, fmt.Errorf("index %q: non-monotonic offset at entry %d (%d <= %d)",
				archiveName, i, idx.offsets[i], prev)
		}
	}
	if chunkCount > 0 {
		idx.total = idx.offsets[chunkCount-1]
	}
	return idx, nil
}

// chunkRange returns the [start,end) byte span of chunk i in the reconstructed
// stream.
func (idx *didxIndex) chunkRange(i int) (start, end uint64) {
	if i > 0 {
		start = idx.offsets[i-1]
	}
	return start, idx.offsets[i]
}
