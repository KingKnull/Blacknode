package store

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	_ "modernc.org/sqlite"
)

// benchDB builds an in-memory database on the production schema. The benchmark
// used to concatenate its own DDL fragments; db.Migrate keeps it honest about
// what the app actually queries, indexes included — an index missing here would
// make a benchmark look slower than production, or faster.
func benchDB(b *testing.B) *sql.DB {
	b.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })
	return conn
}

func BenchmarkHosts_List_500(b *testing.B) {
	db := benchDB(b)
	s := NewHosts(db)
	for i := 0; i < 500; i++ {
		_, err := s.Create(Host{
			Name:       fmt.Sprintf("host-%04d", i),
			Host:       fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			Username:   "ops",
			AuthMethod: "key",
			Tags:       []string{"prod", "web"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := s.List()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 500 {
			b.Fatalf("len = %d", len(got))
		}
	}
}

// Same shape as List_500 but with no tags — the realistic common case
// for users who don't tag every host. Validates the empty-tags
// short-circuit in scanHost.
func BenchmarkHosts_List_500_NoTags(b *testing.B) {
	db := benchDB(b)
	s := NewHosts(db)
	for i := 0; i < 500; i++ {
		_, err := s.Create(Host{
			Name:       fmt.Sprintf("host-%04d", i),
			Host:       "h",
			Username:   "u",
			AuthMethod: "key",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.List()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHosts_GetByName(b *testing.B) {
	db := benchDB(b)
	s := NewHosts(db)
	for i := 0; i < 500; i++ {
		_, err := s.Create(Host{
			Name:       fmt.Sprintf("host-%04d", i),
			Host:       "h",
			Username:   "u",
			AuthMethod: "key",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.GetByName("host-0250")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHTTPRequests_List_200(b *testing.B) {
	db := benchDB(b)
	s := NewHTTPRequests(db)
	for i := 0; i < 200; i++ {
		folder := "infra"
		if i%3 == 0 {
			folder = "billing"
		}
		_, err := s.Create(HTTPRequest{
			Name:    fmt.Sprintf("req-%03d", i),
			Folder:  folder,
			Method:  "GET",
			URL:     fmt.Sprintf("https://api.example.com/v1/r/%d", i),
			Headers: map[string]string{"Authorization": "Bearer x"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := s.List()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 200 {
			b.Fatalf("len = %d", len(got))
		}
	}
}
