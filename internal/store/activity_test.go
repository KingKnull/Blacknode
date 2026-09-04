package store

import (
	"database/sql"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	_ "modernc.org/sqlite"
)

// newActivityDB builds a real database via the production migration path.
// It used to carry a hand-written copy of the activity table, which meant the
// hash-chain columns existed in production and not here — the class of drift
// that db.Migrate exists to prevent.
func newActivityDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestActivity_RecordDefaults(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	got, err := s.Record(Activity{Source: "vault", Kind: "vault.unlock", Title: "Vault unlocked"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Error("expected id to be generated")
	}
	if got.Level != "info" {
		t.Errorf("default level = %q want info", got.Level)
	}
	if got.At == 0 {
		t.Error("expected At to be populated")
	}
}

func TestActivity_FilterBySourceAndLevel(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	mustRecord := func(src, kind, level, title string) {
		t.Helper()
		if _, err := s.Record(Activity{Source: src, Kind: kind, Level: level, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	mustRecord("vault", "vault.unlock", "info", "a")
	mustRecord("vault", "vault.lock", "info", "b")
	mustRecord("exec", "exec.fail", "error", "c")
	mustRecord("plugin", "plugin.fail", "error", "d")

	got, err := s.List(ActivityFilter{Sources: []string{"vault"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("source filter: got %d want 2", len(got))
	}

	got, err = s.List(ActivityFilter{Levels: []string{"error"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("level filter: got %d want 2", len(got))
	}

	got, err = s.List(ActivityFilter{Sources: []string{"vault", "plugin"}, Levels: []string{"error"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != "plugin" {
		t.Fatalf("combined filter: got %+v", got)
	}
}

func TestActivity_OrderNewestFirst(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	for _, at := range []int64{100, 200, 50, 300} {
		if _, err := s.Record(Activity{Source: "x", Kind: "k", Title: "t", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{300, 200, 100, 50}
	for i, a := range got {
		if a.At != want[i] {
			t.Errorf("[%d] At=%d want %d", i, a.At, want[i])
		}
	}
}

// TestActivity_PurgeOlderThan pins the prefix semantics, which is why only one
// of the two sub-cutoff rows goes.
//
// Rows are recorded with at = 100, 200, 50, 300 → seq 1..4. The first row at or
// after the cutoff of 150 is seq 2, so the purge stops there: seq 1 (at=100) is
// removed and seq 3 (at=50) survives despite being older. Deleting seq 3 would
// leave a hole between seq 2 and seq 4 and make the log unverifiable, which is
// too high a price for tidying up one row. When `at` is the insertion time — the
// only way the app itself records activity — the two orderings agree and every
// row below the cutoff is removed.
func TestActivity_PurgeOlderThan(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	for _, at := range []int64{100, 200, 50, 300} {
		if _, err := s.Record(Activity{Source: "x", Kind: "k", Title: "t", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PurgeOlderThan(150)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d want 1", n)
	}
	got, _ := s.List(ActivityFilter{})
	if len(got) != 3 {
		t.Fatalf("after purge: %d want 3", len(got))
	}
	st, err := s.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Valid {
		t.Fatalf("purge broke the chain: %s", st.Detail)
	}
}

func seedChain(t *testing.T, a *Activities, n int) []Activity {
	t.Helper()
	out := make([]Activity, 0, n)
	for i := 0; i < n; i++ {
		rec, err := a.Record(Activity{
			Source: "test",
			Kind:   "test.event",
			Title:  "event",
			At:     int64(1700000000 + i),
		})
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestActivity_ChainLinksAndVerifies(t *testing.T) {
	a := NewActivities(newActivityDB(t))
	recs := seedChain(t, a, 5)

	for i, r := range recs {
		if r.Seq != int64(i+1) {
			t.Errorf("row %d: seq = %d, want %d", i, r.Seq, i+1)
		}
		if r.Hash == "" {
			t.Errorf("row %d: empty hash", i)
		}
		if i == 0 {
			if r.PrevHash != "" {
				t.Errorf("first row should have empty prev_hash, got %q", r.PrevHash)
			}
			continue
		}
		if r.PrevHash != recs[i-1].Hash {
			t.Errorf("row %d: prev_hash %q does not link to %q", i, r.PrevHash, recs[i-1].Hash)
		}
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !st.Valid {
		t.Fatalf("fresh chain reported invalid: %s", st.Detail)
	}
	if st.Rows != 5 {
		t.Errorf("Rows = %d, want 5", st.Rows)
	}
	if st.Head != recs[4].Hash {
		t.Errorf("Head = %q, want %q", st.Head, recs[4].Hash)
	}
}

// TestActivity_VerifyDetectsEditedRow is the whole point of the chain: someone
// with database access changes what a log entry says, and the log has to notice.
func TestActivity_VerifyDetectsEditedRow(t *testing.T) {
	conn := newActivityDB(t)
	a := NewActivities(conn)
	recs := seedChain(t, a, 4)

	if _, err := conn.Exec(`UPDATE activity SET title = ? WHERE id = ?`,
		"something much less alarming", recs[1].ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if st.Valid {
		t.Fatal("edited row went undetected")
	}
	if st.BrokenAtSeq != recs[1].Seq {
		t.Errorf("BrokenAtSeq = %d, want %d", st.BrokenAtSeq, recs[1].Seq)
	}
}

// TestActivity_VerifyDetectsDeletedRow covers the other half: removing an entry
// rather than editing it. The successor's prev_hash no longer matches its
// neighbour.
func TestActivity_VerifyDetectsDeletedRow(t *testing.T) {
	conn := newActivityDB(t)
	a := NewActivities(conn)
	recs := seedChain(t, a, 4)

	if _, err := conn.Exec(`DELETE FROM activity WHERE id = ?`, recs[1].ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if st.Valid {
		t.Fatal("deleted row went undetected")
	}
	if st.BrokenAtSeq != recs[2].Seq {
		t.Errorf("BrokenAtSeq = %d, want %d", st.BrokenAtSeq, recs[2].Seq)
	}
}

// TestActivity_VerifyDetectsRehashedRow is the case worth having. An attacker
// who knows the scheme edits a row and recomputes that row's own hash, so the
// row is internally consistent — Verify's per-row check passes. It still fails,
// because the next row's prev_hash commits to the value that was there before.
func TestActivity_VerifyDetectsRehashedRow(t *testing.T) {
	conn := newActivityDB(t)
	a := NewActivities(conn)
	recs := seedChain(t, a, 4)

	forged := recs[1]
	forged.Title = "rewritten"
	forged.Hash = chainHash(forged)
	if _, err := conn.Exec(`UPDATE activity SET title = ?, hash = ? WHERE id = ?`,
		forged.Title, forged.Hash, forged.ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	// Sanity-check that the forgery is self-consistent, so the test is really
	// exercising the chain link and not just the per-row hash.
	rows, err := a.ExportRange(forged.Seq, forged.Seq)
	if err != nil || len(rows) != 1 {
		t.Fatalf("export: %v (%d rows)", err, len(rows))
	}
	if chainHash(rows[0]) != rows[0].Hash {
		t.Fatal("forged row is not self-consistent; test would pass for the wrong reason")
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if st.Valid {
		t.Fatal("rehashed row went undetected")
	}
	if st.BrokenAtSeq != recs[2].Seq {
		t.Errorf("BrokenAtSeq = %d, want %d (the successor is what catches it)",
			st.BrokenAtSeq, recs[2].Seq)
	}
}

// TestActivity_LegacyRowsAreReportedNotJudged: an install that predates the
// chain has rows with no hash. Verify must neither claim they're valid nor flag
// the whole log as tampered.
func TestActivity_LegacyRowsAreReportedNotJudged(t *testing.T) {
	conn := newActivityDB(t)
	a := NewActivities(conn)

	if _, err := conn.Exec(
		`INSERT INTO activity (id, source, kind, level, title, body, host_id, host_name, at, seq, prev_hash, hash)
		 VALUES ('legacy', 'old', 'old.event', 'info', 'from before', '', '', '', 1600000000, 0, '', '')`,
	); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	recs := seedChain(t, a, 2)

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !st.Valid {
		t.Fatalf("legacy row broke verification: %s", st.Detail)
	}
	if st.UnchainedLegacy != 1 {
		t.Errorf("UnchainedLegacy = %d, want 1", st.UnchainedLegacy)
	}
	if st.Rows != 2 {
		t.Errorf("Rows = %d, want 2 (legacy row excluded)", st.Rows)
	}
	if st.FirstVerifiableSeq != recs[0].Seq {
		t.Errorf("FirstVerifiableSeq = %d, want %d", st.FirstVerifiableSeq, recs[0].Seq)
	}
}

// TestActivity_VerifySurvivesPurge: trimming old rows truncates the front of the
// chain. That's legitimate cleanup, so the remainder must still verify.
func TestActivity_VerifySurvivesPurge(t *testing.T) {
	a := NewActivities(newActivityDB(t))
	recs := seedChain(t, a, 6)

	n, err := a.PurgeOlderThan(recs[3].At)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 3 {
		t.Fatalf("purged %d rows, want 3", n)
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !st.Valid {
		t.Fatalf("purged chain reported invalid: %s", st.Detail)
	}
	if st.FirstVerifiableSeq != recs[3].Seq {
		t.Errorf("FirstVerifiableSeq = %d, want %d", st.FirstVerifiableSeq, recs[3].Seq)
	}
}

// TestActivity_ConcurrentRecordDoesNotFork: two goroutines appending must not
// read the same predecessor. If they did, both would claim the same seq and the
// unique index would reject one — so an error here is as much a failure as an
// invalid chain.
func TestActivity_ConcurrentRecordDoesNotFork(t *testing.T) {
	a := NewActivities(newActivityDB(t))

	const n = 40
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := a.Record(Activity{Source: "test", Kind: "concurrent", Title: "x"})
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent record: %v", err)
		}
	}

	st, err := a.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !st.Valid {
		t.Fatalf("concurrent appends produced an invalid chain: %s", st.Detail)
	}
	if st.Rows != n {
		t.Errorf("Rows = %d, want %d", st.Rows, n)
	}
}
