package store

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const httpRequestsSchema = `
CREATE TABLE http_requests (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    folder TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT 'GET',
    url TEXT NOT NULL,
    headers TEXT NOT NULL DEFAULT '{}',
    body TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    insecure INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);`

func newHTTPRequestsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(httpRequestsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHTTPRequest_CreateRoundTrip(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	saved, err := s.Create(HTTPRequest{
		Name:    "health",
		Folder:  "infra",
		Method:  "GET",
		URL:     "https://example.com/health",
		Headers: map[string]string{"Authorization": "Bearer x"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated id")
	}
	got, err := s.Get(saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != "https://example.com/health" {
		t.Errorf("url roundtrip mismatch: %q", got.URL)
	}
	if got.Headers["Authorization"] != "Bearer x" {
		t.Errorf("header roundtrip mismatch: %+v", got.Headers)
	}
	if got.Folder != "infra" {
		t.Errorf("folder roundtrip mismatch: %q", got.Folder)
	}
}

func TestHTTPRequest_DefaultsAndValidation(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	if _, err := s.Create(HTTPRequest{URL: "https://x"}); err == nil {
		t.Error("expected error when name missing")
	}
	if _, err := s.Create(HTTPRequest{Name: "n"}); err == nil {
		t.Error("expected error when url missing")
	}
	got, err := s.Create(HTTPRequest{Name: "n", URL: "https://x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Method != "GET" {
		t.Errorf("default method = %q want GET", got.Method)
	}
}

func TestHTTPRequest_InsecureRoundTrip(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	saved, err := s.Create(HTTPRequest{Name: "n", URL: "https://x", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Insecure {
		t.Fatal("insecure flag did not round-trip")
	}
}

func TestHTTPRequest_Update(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	saved, _ := s.Create(HTTPRequest{Name: "n", URL: "https://x"})
	saved.Method = "POST"
	saved.URL = "https://y"
	saved.Headers = map[string]string{"X": "1"}
	if err := s.Update(saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.Get(saved.ID)
	if got.Method != "POST" || got.URL != "https://y" || got.Headers["X"] != "1" {
		t.Fatalf("update mismatch: %+v", got)
	}
}

func TestHTTPRequest_UpdateRequiresID(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	if err := s.Update(HTTPRequest{Name: "n", URL: "https://x"}); err == nil {
		t.Error("expected error when updating without id")
	}
}

func TestHTTPRequest_ListOrder(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	for _, r := range []HTTPRequest{
		{Name: "zebra", URL: "https://x", Folder: "b"},
		{Name: "alpha", URL: "https://x", Folder: "a"},
		{Name: "Beta", URL: "https://x", Folder: "a"},
	} {
		if _, err := s.Create(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	// Folder a comes before b; within folder a, Beta < alpha by NOCASE.
	want := []string{"alpha", "Beta", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i, r := range got {
		if r.Name != want[i] {
			t.Errorf("[%d] %q want %q", i, r.Name, want[i])
		}
	}
}

func TestHTTPRequest_Delete(t *testing.T) {
	s := NewHTTPRequests(newHTTPRequestsDB(t))
	saved, _ := s.Create(HTTPRequest{Name: "n", URL: "https://x"})
	if err := s.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(saved.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
