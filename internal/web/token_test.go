package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedTokenPersists(t *testing.T) {
	t.Setenv("WEB_TOKEN", "")
	dir := t.TempDir()
	first, created, err := loadWebToken(dir)
	if err != nil || !created {
		t.Fatalf("first token: created=%v err=%v", created, err)
	}
	if len(first) != 64 {
		t.Fatalf("token size=%d, expected 64", len(first))
	}
	second, created, err := loadWebToken(dir)
	if err != nil || created || second != first {
		t.Fatalf("token did not persist: second=%q created=%v err=%v", second, created, err)
	}
	info, err := os.Stat(filepath.Join(dir, "web-token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token permissions=%#o, want 0600", info.Mode().Perm())
	}
}

func TestProvidedTokenTakesPrecedence(t *testing.T) {
	t.Setenv("WEB_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")
	token, created, err := loadWebToken(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil || created || token != os.Getenv("WEB_TOKEN") {
		t.Fatalf("explicit token not respected: token=%q created=%v err=%v", token, created, err)
	}
}

func TestShortExplicitTokenRejected(t *testing.T) {
	t.Setenv("WEB_TOKEN", "short")
	if _, _, err := loadWebToken(t.TempDir()); err == nil {
		t.Fatal("expected short token error")
	}
}
