package pbscommon

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// ErrReadCancelled is returned by a DIDXReaderAt read once its cancel predicate
// reports true, so a long lazy walk (e.g. a cross-snapshot search) can be
// aborted between chunk fetches instead of running to completion.
var ErrReadCancelled = errors.New("read cancelled")

// DIDXReaderAt is an io.ReaderAt over a PBS dynamic-index archive that fetches
// referenced chunks from the server ON DEMAND, with a small LRU cache, instead
// of downloading and reassembling the whole archive into a temp file first.
//
// This is what makes selective restore cheap: PXARReader.walk skips the payload
// of files the caller did not select (it never ReadAt's those byte ranges), so
// the chunks that hold only unselected payload are never fetched. It also removes
// the "free %TEMP% space == archive size" requirement of the temp-file path,
// which is the prerequisite for backing up large drives without splitting.
//
// A cache is mandatory, not an optimization: walking reads many tiny headers
// (16 bytes) that fall inside the same multi-MB chunk, so without caching the
// last-used chunks every header read would re-download and re-decompress a whole
// chunk.
type DIDXReaderAt struct {
	pbs      *PBSClient
	idx      *didxIndex
	cache    *chunkCache
	progress func(fetched, total int)
	cancel   func() bool // optional; when it returns true, reads abort with ErrReadCancelled

	mu      sync.Mutex // serializes fetch bookkeeping (fetched counter)
	fetched int
}

// SetCancelCheck installs a predicate polled before each read and chunk fetch;
// once it returns true the reader aborts with ErrReadCancelled. Pass nil to
// disable. Set it immediately after construction and before the first read —
// it is read without locking and is not safe to change concurrently with reads.
func (r *DIDXReaderAt) SetCancelCheck(fn func() bool) { r.cancel = fn }

// NewDIDXReaderAt downloads and parses the .didx index for archiveName and
// returns a lazy reader over the reconstructed stream plus its total size. The
// PBSClient must stay Connected (reader session) for the lifetime of the reader,
// since chunks are fetched as the caller reads. cacheChunks defaults to 32.
// progress (optional) is called with (chunksFetchedSoFar, totalChunks) each time
// a NEW chunk is fetched from the server (cache hits do not advance it).
func (pbs *PBSClient) NewDIDXReaderAt(archiveName string, cacheChunks int, progress func(fetched, total int)) (*DIDXReaderAt, int64, error) {
	if cacheChunks <= 0 {
		cacheChunks = 32
	}
	idx, err := pbs.downloadDIDXIndex(archiveName)
	if err != nil {
		return nil, 0, err
	}
	return &DIDXReaderAt{
		pbs:      pbs,
		idx:      idx,
		cache:    newChunkCache(cacheChunks),
		progress: progress,
	}, int64(idx.total), nil
}

// chunkIndexAt returns the index of the chunk whose [start,end) span contains pos.
func (r *DIDXReaderAt) chunkIndexAt(pos uint64) int {
	// offsets[i] is the cumulative END offset of chunk i (ascending), so the chunk
	// containing pos is the first one whose end offset is strictly greater than pos.
	return sort.Search(len(r.idx.offsets), func(i int) bool {
		return r.idx.offsets[i] > pos
	})
}

// chunkAt returns the decompressed bytes of chunk ci, from cache or by fetching
// it from the server (verifying size and SHA-256 against the index digest).
func (r *DIDXReaderAt) chunkAt(ci int) ([]byte, error) {
	if r.cancel != nil && r.cancel() {
		return nil, ErrReadCancelled
	}
	if data, ok := r.cache.get(ci); ok {
		return data, nil
	}
	digest := r.idx.digests[ci]
	chunk, err := r.pbs.GetChunkData(digest)
	if err != nil {
		return nil, fmt.Errorf("fetch chunk %s (index %d/%d): %w", digest, ci, len(r.idx.digests), err)
	}
	start, end := r.idx.chunkRange(ci)
	if uint64(len(chunk)) != end-start {
		return nil, fmt.Errorf("chunk %s (index %d): decompressed size %d != expected %d", digest, ci, len(chunk), end-start)
	}
	// PBS dynamic-index digests are the SHA-256 of the chunk plaintext, or the
	// id_key-salted digest for an encrypted snapshot (see PBSClient.ChunkDigest).
	// A mismatch means a corrupted or tampered chunk — fail rather than serve
	// wrong data.
	if r.pbs.ChunkDigestHex(chunk) != digest {
		return nil, fmt.Errorf("chunk %s (index %d): content hash mismatch", digest, ci)
	}
	r.cache.put(ci, chunk)

	r.mu.Lock()
	r.fetched++
	fetched := r.fetched
	r.mu.Unlock()
	if r.progress != nil {
		r.progress(fetched, len(r.idx.digests))
	}
	return chunk, nil
}

// ReadAt implements io.ReaderAt over the reconstructed stream, fetching only the
// chunks that overlap [off, off+len(p)). It satisfies the io.ReaderAt contract:
// it fills p fully unless it reaches the end of the stream, in which case it
// returns the bytes read and io.EOF.
func (r *DIDXReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if r.cancel != nil && r.cancel() {
		return 0, ErrReadCancelled
	}
	if off < 0 {
		return 0, fmt.Errorf("didx readerat: negative offset %d", off)
	}
	total := int64(r.idx.total)
	if off >= total {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		pos := uint64(off) + uint64(n)
		if pos >= r.idx.total {
			break
		}
		ci := r.chunkIndexAt(pos)
		chunk, err := r.chunkAt(ci)
		if err != nil {
			return n, err
		}
		start, _ := r.idx.chunkRange(ci)
		n += copy(p[n:], chunk[pos-start:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// chunkCache is a small LRU cache of decompressed chunks keyed by chunk index.
type chunkCache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List
	items map[int]*list.Element
}

type chunkCacheEntry struct {
	idx  int
	data []byte
}

func newChunkCache(capacity int) *chunkCache {
	return &chunkCache{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[int]*list.Element, capacity),
	}
}

func (c *chunkCache) get(idx int) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[idx]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*chunkCacheEntry).data, true
	}
	return nil, false
}

func (c *chunkCache) put(idx int, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[idx]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*chunkCacheEntry).data = data
		return
	}
	el := c.ll.PushFront(&chunkCacheEntry{idx: idx, data: data})
	c.items[idx] = el
	for c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*chunkCacheEntry).idx)
	}
}
