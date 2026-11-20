package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadWebToken uses an explicit secret when provided, otherwise generates a
// stable random secret on first start and stores it in the persisted data dir.
func loadWebToken(home string) (token string, generated bool, err error) {
	if token = os.Getenv("WEB_TOKEN"); token != "" {
		if len(token) < 24 {
			return "", false, errors.New("WEB_TOKEN must contain at least 24 characters")
		}
		return token, false, nil
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", false, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(home, "web-token")
	if existing, err := os.ReadFile(path); err == nil {
		token = strings.TrimSpace(string(existing))
		if len(token) < 24 {
			return "", false, fmt.Errorf("saved web token is invalid; provide WEB_TOKEN or fix %s", path)
		}
		return token, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read web token: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, fmt.Errorf("generate web token: %w", err)
	}
	token = hex.EncodeToString(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", false, readErr
		}
		token = strings.TrimSpace(string(content))
		if len(token) < 24 {
			return "", false, errors.New("saved web token is invalid")
		}
		return token, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("create web token: %w", err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		return "", false, fmt.Errorf("save web token: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close web token: %w", err)
	}
	return token, true, nil
}
