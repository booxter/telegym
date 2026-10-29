package telegym

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func readRecords(t *testing.T, path string) []userRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var recs []userRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r userRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", sc.Text(), err)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return recs
}

// TestAppendUserWritesRecord checks the on-disk NDJSON schema that later
// scenarios read back to replay registered users.
func TestAppendUserWritesRecord(t *testing.T) {
	api := &API{client: NewClient()}
	path := filepath.Join(t.TempDir(), "nested", "dir", "users.ndjson")

	u := &User{ChatID: 1001, Username: "alice", FirstName: "Alice"}
	opts := AppendOpts{Tags: []string{"registered"}, Attrs: map[string]string{"country": "UA"}}
	if err := api.AppendUser(path, u, opts); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	if err := api.AppendUser(path, &User{ChatID: 1002}, AppendOpts{}); err != nil {
		t.Fatalf("second AppendUser: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	got := recs[0]
	if got.ChatID != 1001 || got.Username != "alice" || got.FirstName != "Alice" {
		t.Errorf("identity fields = %+v", got)
	}
	if got.SavedAt == 0 {
		t.Error("SavedAt is not set")
	}
	if !reflect.DeepEqual(got.Tags, opts.Tags) || !reflect.DeepEqual(got.Attrs, opts.Attrs) {
		t.Errorf("tags/attrs = %v/%v, want %v/%v", got.Tags, got.Attrs, opts.Tags, opts.Attrs)
	}
	if recs[1].ChatID != 1002 {
		t.Errorf("second record ChatID = %d, want 1002", recs[1].ChatID)
	}
}

// TestAppendUserConcurrent runs many VU-like goroutines against one file.
// Interleaved writes would show up as broken JSON lines or a short count.
func TestAppendUserConcurrent(t *testing.T) {
	api := &API{client: NewClient()}
	path := filepath.Join(t.TempDir(), "users.ndjson")

	const n = 200
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if err := api.AppendUser(path, &User{ChatID: int64(i + 1)}, AppendOpts{}); err != nil {
				t.Errorf("AppendUser(%d): %v", i, err)
			}
		})
	}
	wg.Wait()

	recs := readRecords(t, path)
	if len(recs) != n {
		t.Fatalf("got %d records, want %d", len(recs), n)
	}
	seen := make(map[int64]bool, n)
	for _, r := range recs {
		seen[r.ChatID] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct chat IDs, want %d", len(seen), n)
	}
}

func TestAppendUserErrors(t *testing.T) {
	api := &API{client: NewClient()}
	dir := t.TempDir()

	// A regular file where a parent directory is expected makes MkdirAll fail.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path string
		user *User
	}{
		"nil user":         {path: filepath.Join(dir, "u.ndjson"), user: nil},
		"empty path":       {path: "", user: &User{ChatID: 1}},
		"parent is a file": {path: filepath.Join(blocker, "u.ndjson"), user: &User{ChatID: 1}},
		"path is a dir":    {path: dir, user: &User{ChatID: 1}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := api.AppendUser(tc.path, tc.user, AppendOpts{}); err == nil {
				t.Error("AppendUser returned nil error")
			}
		})
	}
}

// TestLockForReusesMutex: the per-path lock is what serializes writers, so
// the same path must always map to the same mutex.
func TestLockForReusesMutex(t *testing.T) {
	a := lockFor("/tmp/telegym-lock-test-a")
	if lockFor("/tmp/telegym-lock-test-a") != a {
		t.Error("lockFor returned a different mutex for the same path")
	}
	if lockFor("/tmp/telegym-lock-test-b") == a {
		t.Error("lockFor shared one mutex between different paths")
	}
}
