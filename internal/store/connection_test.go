package store

import (
	"testing"
)

func TestLastConnectionPersistsAndClears(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil { t.Fatal(err) }
	if err := s.Upsert(SubEntry{Name:"primary", Links: []string{"trojan://pw@example.com:443#first"}}); err != nil { t.Fatal(err) }
	linkHash := HashLink("trojan://pw@example.com:443#first")
	choice := &ConnectionPreference{Subscription:"primary", LinkHash:linkHash}
	if err := s.SetLastConnection(choice); err != nil { t.Fatal(err) }
	restored, err := Open(dir)
	if err != nil { t.Fatal(err) }
	if got := restored.LastConnection(); got == nil || *got != *choice {
		t.Fatalf("invalid restored choice: %+v", got)
	}
	if err := restored.SetLastConnection(nil); err != nil { t.Fatal(err) }
	cleared, err := Open(dir)
	if err != nil { t.Fatal(err) }
	if cleared.LastConnection() != nil { t.Fatal("explicit disconnect did not persist") }
}

func TestRemoveSubscriptionClearsConnection(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil { t.Fatal(err) }
	if err := s.Upsert(SubEntry{Name:"primary"}); err != nil { t.Fatal(err) }
	if err := s.SetLastConnection(&ConnectionPreference{Subscription:"primary",LinkHash:HashLink("link")}); err != nil { t.Fatal(err) }
	if err := s.Remove("primary"); err != nil { t.Fatal(err) }
	after, err := Open(dir)
	if err != nil { t.Fatal(err) }
	if after.LastConnection() != nil { t.Fatal("deleted subscription still auto-connects") }
}
