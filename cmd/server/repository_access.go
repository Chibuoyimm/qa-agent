package main

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// Only private repository connections that request saved access use this local key.
func repositoryKey() ([]byte, error) {
	dir := os.Getenv("QA_REPOSITORY_STORAGE_DIR")
	if dir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(root, "qa-agent", "repository-access")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("repository access directory must be writable only by the server user")
	}
	path := filepath.Join(dir, "encryption.key")
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return nil, errors.New("repository key requires a private file")
			}
			key, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if len(key) != 32 {
				return nil, errors.New("invalid repository key")
			}
			return key, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		file, err := os.CreateTemp(dir, ".key-")
		if err != nil {
			return nil, err
		}
		_, err = file.Write(key)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Link(file.Name(), path)
		}
		os.Remove(file.Name())
		if err == nil {
			return key, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
}
