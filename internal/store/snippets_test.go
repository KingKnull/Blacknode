package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const snippetsSchema = `
CREATE TABLE snippets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    body TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);`

func newSnippetsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(snippetsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSnippetCreateAndGet(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	sn, err := s.Create(Snippet{
		Name:        "restart nginx",
		Body:        "sudo systemctl restart {{service|nginx}}",
		Description: "Restart a systemd service",
		Tags:        []string{"ops", "systemd"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sn.ID == "" {
		t.Fatal("expected generated ID")
	}
	if sn.CreatedAt == 0 || sn.UpdatedAt == 0 {
		t.Fatal("expected auto-generated timestamps")
	}

	got, err := s.Get(sn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "restart nginx" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.Body != "sudo systemctl restart {{service|nginx}}" {
		t.Fatalf("body = %q", got.Body)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "ops" || got.Tags[1] != "systemd" {
		t.Fatalf("tags = %v", got.Tags)
	}
}

func TestSnippetCreateValidation(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	if _, err := s.Create(Snippet{Body: "body"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := s.Create(Snippet{Name: "name"}); err == nil {
		t.Fatal("expected error for missing body")
	}
}

func TestSnippetUpdate(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	sn, _ := s.Create(Snippet{
		Name: "test",
		Body: "echo hello",
		Tags: []string{"a"},
	})

	sn.Name = "updated"
	sn.Tags = []string{"b", "c"}
	if err := s.Update(sn); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(sn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "updated" {
		t.Fatalf("name = %q after update", got.Name)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "b" {
		t.Fatalf("tags = %v after update", got.Tags)
	}
}

func TestSnippetUpdateRequiresID(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))
	if err := s.Update(Snippet{Name: "x", Body: "y"}); err == nil {
		t.Fatal("expected error when updating without ID")
	}
}

func TestSnippetListOrdersByName(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	for _, n := range []string{"zebra", "alpha", "Mango"} {
		if _, err := s.Create(Snippet{Name: n, Body: "cmd"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	want := []string{"alpha", "Mango", "zebra"}
	for i, sn := range got {
		if sn.Name != want[i] {
			t.Errorf("[%d] %s want %s", i, sn.Name, want[i])
		}
	}
}

func TestSnippetDelete(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	sn, _ := s.Create(Snippet{Name: "del", Body: "rm -rf /"})
	if err := s.Delete(sn.ID); err != nil {
		t.Fatal(err)
	}

	_, err := s.Get(sn.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSnippetNilTagsBecomesEmptySlice(t *testing.T) {
	s := NewSnippets(newSnippetsDB(t))

	sn, _ := s.Create(Snippet{Name: "notags", Body: "ls"})
	got, err := s.Get(sn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tags == nil {
		t.Fatal("expected non-nil tags slice")
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected empty tags, got %v", got.Tags)
	}
}
