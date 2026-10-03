package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The bug this file covers: the hash-chain columns were added with
// `ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`, so every row an existing
// install already had ended up at seq = 0, and the UNIQUE index created
// immediately afterwards then failed. Migrate returned an error, Open returned
// an error, and main.go's log.Fatalf turned that into an app that builds fine
// and exits at startup. Only a database with fewer than two pre-chain activity
// rows escaped it, which is why it did not show up on a fresh install.

// legacyActivitySchema is the activity table as it existed before the chain
// columns — deliberately a copy rather than a reference to `schema`, because
// the point is to reproduce an old database, and it must not drift forward
// when the current schema changes.
const legacyActivitySchema = `
CREATE TABLE activity (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    kind TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL
);
`

// newLegacyDB builds a database in the pre-chain shape with n activity rows.
func newLegacyDB(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(legacyActivitySchema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := conn.Exec(
			`INSERT INTO activity (id, source, kind, level, title, at) VALUES (?, 'vault', 'vault.unlock', 'info', 'unlocked', ?)`,
			"id-"+string(rune('a'+i)), 1700000000+int64(i),
		); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestMigrate_UpgradesLegacyActivityRows is the regression test. Three rows is
// the smallest number that is unambiguously more than the one the unique index
// tolerates.
func TestMigrate_UpgradesLegacyActivityRows(t *testing.T) {
	path := newLegacyDB(t, 3)

	d, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath on a pre-chain database failed: %v", err)
	}
	defer d.Close()

	// Every row must have a distinct seq, and they must be 1..N in insertion
	// order — the chain continues from the highest, so a gap or a repeat here
	// becomes a permanently unverifiable log.
	rows, err := d.Query(`SELECT seq, hash, prev_hash FROM activity ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var seq int64
		var hash, prev string
		if err := rows.Scan(&seq, &hash, &prev); err != nil {
			t.Fatal(err)
		}
		got = append(got, seq)
		// Legacy rows keep an empty hash on purpose: they were written before
		// the chain existed, and hashing them now would assert a provenance
		// they do not have. Verify reports them as unchained rather than valid.
		if hash != "" || prev != "" {
			t.Errorf("seq %d was given hash %q / prev %q; legacy rows must stay unchained", seq, hash, prev)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for i, seq := range got {
		if seq != int64(i+1) {
			t.Errorf("row %d has seq %d, want %d (rowid order)", i, seq, i+1)
		}
	}

	// The index the migration was failing to create must now exist, since it is
	// what makes a duplicate seq impossible from here on.
	var idx int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_activity_seq'`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Error("idx_activity_seq was not created")
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	path := newLegacyDB(t, 3)

	d, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()

	// A second open must not renumber anything or fail on the existing index.
	d2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("second OpenPath failed: %v", err)
	}
	defer d2.Close()

	var maxSeq, distinct, total int64
	if err := d2.QueryRow(`SELECT COALESCE(MAX(seq),0), COUNT(DISTINCT seq), COUNT(*) FROM activity`).Scan(&maxSeq, &distinct, &total); err != nil {
		t.Fatal(err)
	}
	if maxSeq != 3 || distinct != 3 || total != 3 {
		t.Errorf("after a second migrate: max=%d distinct=%d total=%d, want 3/3/3", maxSeq, distinct, total)
	}
}

func TestMigrate_LegacyEdgeCounts(t *testing.T) {
	// One row and zero rows both used to pass by accident — a single seq = 0 is
	// unique, and an empty table has nothing to collide. Pinned so the backfill
	// is not "fixed" into something that only handles the many-row case.
	for _, n := range []int{0, 1, 2, 50} {
		t.Run(string(rune('0'+n/10))+string(rune('0'+n%10)), func(t *testing.T) {
			path := newLegacyDB(t, n)
			d, err := OpenPath(path)
			if err != nil {
				t.Fatalf("%d legacy rows: %v", n, err)
			}
			defer d.Close()

			var distinct, total, zeroes int64
			if err := d.QueryRow(`SELECT COUNT(DISTINCT seq), COUNT(*), COALESCE(SUM(seq=0),0) FROM activity`).Scan(&distinct, &total, &zeroes); err != nil {
				t.Fatal(err)
			}
			if total != int64(n) {
				t.Errorf("row count changed: got %d, want %d", total, n)
			}
			if distinct != total {
				t.Errorf("%d rows share a seq", total-distinct)
			}
			if zeroes != 0 {
				t.Errorf("%d rows were left at seq 0", zeroes)
			}
		})
	}
}

// TestMigrate_FreshDatabase is the case that always worked, kept so a change to
// the backfill cannot break it.
func TestMigrate_FreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	d, err := OpenPath(path)
	if err != nil {
		t.Fatalf("fresh database: %v", err)
	}
	defer d.Close()

	var n int64
	if err := d.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("fresh database has %d activity rows", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file was not created: %v", err)
	}
}

// newCollapsedDB reproduces a database that ran the first attempt at the seq
// repair: it renumbered with a correlated subquery over the table being
// updated, and every row read the first row's new value, so all n rows ended up
// at seq = 1 instead of 1..N. The sentinel the repair keyed on is gone, which is
// why a fix matching `seq = 0` leaves this database exactly as broken as it
// found it.
func newCollapsedDB(t *testing.T, n int) string {
	t.Helper()
	path := newLegacyDB(t, n)
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, ddl := range []string{
		`ALTER TABLE activity ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE activity ADD COLUMN prev_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activity ADD COLUMN hash TEXT NOT NULL DEFAULT ''`,
		`UPDATE activity SET seq = 1`,
	} {
		if _, err := conn.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestMigrate_RepairsCollapsedSeq is the second regression test, for the state a
// real install was left in: three unchained rows all sharing seq = 1, and no
// unique index.
func TestMigrate_RepairsCollapsedSeq(t *testing.T) {
	for _, n := range []int{2, 3, 10} {
		t.Run(string(rune('0'+n/10))+string(rune('0'+n%10)), func(t *testing.T) {
			path := newCollapsedDB(t, n)

			d, err := OpenPath(path)
			if err != nil {
				t.Fatalf("OpenPath on a collapsed database failed: %v", err)
			}
			defer d.Close()

			rows, err := d.Query(`SELECT seq FROM activity ORDER BY rowid`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got []int64
			for rows.Next() {
				var seq int64
				if err := rows.Scan(&seq); err != nil {
					t.Fatal(err)
				}
				got = append(got, seq)
			}
			if len(got) != n {
				t.Fatalf("got %d rows, want %d", len(got), n)
			}
			for i, seq := range got {
				if seq != int64(i+1) {
					t.Errorf("row %d has seq %d, want %d", i, seq, i+1)
				}
			}

			var idx int
			if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_activity_seq'`).Scan(&idx); err != nil {
				t.Fatal(err)
			}
			if idx != 1 {
				t.Error("idx_activity_seq was not created")
			}
		})
	}
}

// TestMigrate_CollapsedIsIdempotent guards the repair against renumbering a
// database it has already repaired.
func TestMigrate_CollapsedIsIdempotent(t *testing.T) {
	path := newCollapsedDB(t, 4)
	d, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()

	d2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("second OpenPath failed: %v", err)
	}
	defer d2.Close()

	var maxSeq, distinct, total int64
	if err := d2.QueryRow(`SELECT COALESCE(MAX(seq),0), COUNT(DISTINCT seq), COUNT(*) FROM activity`).Scan(&maxSeq, &distinct, &total); err != nil {
		t.Fatal(err)
	}
	if maxSeq != 4 || distinct != 4 || total != 4 {
		t.Errorf("after a second migrate: max=%d distinct=%d total=%d, want 4/4/4", maxSeq, distinct, total)
	}
}

// TestMigrate_LeavesChainedRowsAlone covers the database the previous fix would
// have broken: one pre-chain row at seq = 0 alongside rows the app recorded
// after the chain went in. Renumbering the legacy row to 1 collides with a real
// chained row, and moving it above the chain would make Purge treat the oldest
// row in the table as the newest. The values are already distinct, so the right
// answer is to write nothing.
func TestMigrate_LeavesChainedRowsAlone(t *testing.T) {
	path := newLegacyDB(t, 1)
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`ALTER TABLE activity ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE activity ADD COLUMN prev_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activity ADD COLUMN hash TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX idx_activity_seq ON activity(seq)`,
	} {
		if _, err := conn.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, err := conn.Exec(
			`INSERT INTO activity (id, source, kind, level, title, at, seq, prev_hash, hash)
			 VALUES (?, 'vault', 'vault.unlock', 'info', 'unlocked', ?, ?, ?, ?)`,
			"chained-"+string(rune('a'+i)), 1800000000+int64(i), i, "prev", "hash-"+string(rune('a'+i)),
		); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	d, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath on a chained database failed: %v", err)
	}
	defer d.Close()

	rows, err := d.Query(`SELECT seq, hash FROM activity ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		seq  int64
		hash string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.seq, &r.hash); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := []row{{0, ""}, {1, "hash-b"}, {2, "hash-c"}, {3, "hash-d"}}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestPlanActivitySeq exercises the decision without a database, since that is
// where the interesting cases are and building each one as real SQL obscures
// them.
func TestPlanActivitySeq(t *testing.T) {
	legacy := func(seq int64) activityRow { return activityRow{id: "l", seq: seq} }
	chained := func(seq int64) activityRow { return activityRow{id: "c", seq: seq, chained: true} }

	cases := []struct {
		name string
		in   []activityRow
		want []int64
	}{
		{"empty", nil, nil},
		{"single pre-chain row is normalised off zero", []activityRow{legacy(0)}, []int64{1}},
		{"all pre-chain", []activityRow{legacy(0), legacy(0), legacy(0)}, []int64{1, 2, 3}},

		// The state a real install reached.
		{"collapsed to one", []activityRow{legacy(1), legacy(1), legacy(1)}, []int64{1, 2, 3}},

		// No chained row, so nothing depends on the values and rowid order wins
		// even where the existing values are already distinct.
		{"unchained out of order", []activityRow{legacy(9), legacy(4), legacy(7)}, []int64{1, 2, 3}},

		{"chained rows are never moved", []activityRow{chained(1), chained(2)}, []int64{1, 2}},
		{"a distinct legacy zero is left alone", []activityRow{legacy(0), chained(1), chained(2)}, []int64{0, 1, 2}},

		// Only the colliding rows move, and they take the lowest free slots
		// rather than stacking above the chain.
		{
			"colliding unchained rows move around the chain",
			[]activityRow{legacy(5), legacy(5), chained(2), chained(3)},
			[]int64{1, 4, 2, 3},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := planActivitySeq(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
			// Whatever it decides, the result must satisfy the index.
			seen := map[int64]bool{}
			for _, s := range got {
				if seen[s] {
					t.Errorf("plan repeats seq %d: %v", s, got)
				}
				seen[s] = true
			}
			// And it must never move a row a hash commits to.
			for i, r := range c.in {
				if r.chained && got[i] != r.seq {
					t.Errorf("chained row %d moved from seq %d to %d", i, r.seq, got[i])
				}
			}
		})
	}
}

// TestPlanActivitySeq_UnrepairableChain covers the one state with no correct
// answer. It should not be reachable — the unique index makes duplicate chained
// seqs impossible once it exists, and no row gets a hash before it does — but a
// named error beats a bare "UNIQUE constraint failed" if it ever happens.
func TestPlanActivitySeq_UnrepairableChain(t *testing.T) {
	in := []activityRow{
		{id: "a", seq: 7, chained: true},
		{id: "b", seq: 7, chained: true},
	}
	_, err := planActivitySeq(in)
	if err == nil {
		t.Fatal("two chained rows sharing a seq must be reported, not silently renumbered")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("error should name the seq involved, got: %v", err)
	}
}
