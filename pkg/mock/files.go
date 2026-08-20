package mock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// FileStore keeps multipart-uploaded media bytes in memory keyed by
// generated file_id, so telegym-proxy can fetch them later when forwarding to
// real Telegram. Bounded by maxBytes with FIFO eviction - first-in,
// first-out is enough for the real-user mode use case (low traffic,
// human-paced) and avoids the bookkeeping of LRU.
type FileStore struct {
	mu        sync.Mutex
	files     map[string]*storedFile
	order     []string // insertion order, oldest first
	totalSize int64
	maxBytes  int64
	idSeed    atomic.Int64
}

type storedFile struct {
	ID          string
	Kind        string // Telegram media type, e.g. photo, video, or document
	ContentType string
	Filename    string
	Bytes       []byte
	StoredAt    time.Time
}

const defaultMaxFileBytes = 100 * 1024 * 1024 // 100 MB

func NewFileStore() *FileStore {
	return &FileStore{
		files:    map[string]*storedFile{},
		maxBytes: defaultMaxFileBytes,
	}
}

func (s *FileStore) setMaxBytes(maxBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxBytes = maxBytes
	s.evict()
}

func (s *FileStore) maxSize() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxBytes
}

// Put stores bytes and returns a deterministic-looking file_id. Evicts
// oldest entries until total size fits under maxBytes.
func (s *FileStore) Put(kind, contentType, filename string, b []byte) string {
	id := fmt.Sprintf("MOCK_%s_%d", kind, s.idSeed.Add(1))
	f := &storedFile{
		ID:          id,
		Kind:        kind,
		ContentType: contentType,
		Filename:    filename,
		Bytes:       b,
		StoredAt:    time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.files[id] = f
	s.order = append(s.order, id)
	s.totalSize += int64(len(b))

	s.evict()
	return id
}

func (s *FileStore) evict() {
	for s.totalSize > s.maxBytes && len(s.order) > 0 {
		evictID := s.order[0]
		s.order = s.order[1:]
		if evicted, ok := s.files[evictID]; ok {
			s.totalSize -= int64(len(evicted.Bytes))
			delete(s.files, evictID)
		}
	}
}

// Get returns the file if present.
func (s *FileStore) Get(id string) (*storedFile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	return f, ok
}

func (s *FileStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	if !ok {
		return false
	}
	delete(s.files, id)
	s.totalSize -= int64(len(f.Bytes))
	for i, orderedID := range s.order {
		if orderedID == id {
			copy(s.order[i:], s.order[i+1:])
			s.order = s.order[:len(s.order)-1]
			break
		}
	}
	return true
}

// clear removes every retained file and returns the released item and byte
// counts. Capacity configuration and the file ID sequence are preserved.
func (s *FileStore) clear() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.files)
	bytes := s.totalSize
	s.files = map[string]*storedFile{}
	s.order = nil
	s.totalSize = 0
	return count, bytes
}

// Stats reports current usage; used by /debug/files (no-arg) for visibility.
func (s *FileStore) Stats() (count int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.files), s.totalSize
}
