package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryKeySurvivesConcurrentFirstUse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QA_REPOSITORY_STORAGE_DIR", dir)
	type result struct {
		key []byte
		err error
	}
	results := make(chan result, 8)
	for range 8 {
		go func() { key, err := repositoryKey(); results <- result{key, err} }()
	}
	var first []byte
	for range 8 {
		got := <-results
		if got.err != nil || len(got.key) != 32 {
			t.Fatalf("repository key: %v", got.err)
		}
		if first == nil {
			first = got.key
		}
		if !bytes.Equal(first, got.key) {
			t.Fatal("keys differ between connections")
		}
	}
	path := filepath.Join(dir, "encryption.key")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryKey(); err == nil {
		t.Fatal("accepted a key readable by other accounts")
	}
}
