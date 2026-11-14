package documents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
)

// LocalStore stores uploads under one directory.
type LocalStore struct {
	dir string
}

// NewLocalStore builds a local file store.
func NewLocalStore(dir string) (*LocalStore, error) {
	if dir == "" {
		return nil, errors.New("documents directory is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create documents directory: %w", err)
	}
	return &LocalStore{dir: dir}, nil
}

// Save copies a stream to a unique file under the store directory.
func (s *LocalStore) Save(filename string, reader io.Reader, maxBytes int64) (appdocuments.StoredFile, error) {
	ext := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	if len(ext) > 12 {
		ext = ""
	}
	file, err := os.CreateTemp(s.dir, "doc-*"+ext)
	if err != nil {
		return appdocuments.StoredFile{}, fmt.Errorf("create upload file: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}

	hash := sha256.New()
	limited := io.LimitReader(reader, maxBytes+1)
	size, err := io.Copy(io.MultiWriter(file, hash), limited)
	if err != nil {
		cleanup()
		return appdocuments.StoredFile{}, fmt.Errorf("write upload: %w", err)
	}
	if size > maxBytes {
		cleanup()
		return appdocuments.StoredFile{}, appdocuments.ErrFileTooLarge
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return appdocuments.StoredFile{}, fmt.Errorf("close upload: %w", err)
	}
	return appdocuments.StoredFile{
		Path:   path,
		Size:   size,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// Delete removes a stored file, refusing paths outside the store directory.
func (s *LocalStore) Delete(path string) error {
	if path == "" {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(s.dir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || strings.HasPrefix(relative, "..") {
		return errors.New("refusing to delete a file outside the documents directory")
	}
	if err := os.Remove(absolute); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
