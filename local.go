package gostorage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localClient implements Client for local filesystem storage.
type localClient struct {
	basePath string
	config   Config
}

// newLocal creates a local filesystem storage client.
func newLocal(cfg Config) (Client, error) {
	basePath := cfg.Endpoint
	if basePath == "" {
		basePath = "."
	}
	basePath = filepath.Clean(basePath)

	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("gostorage local: create base directory: %w", err)
	}

	return &localClient{
		basePath: basePath,
		config:   cfg,
	}, nil
}

// GetPresignedUploadURL implements Client.
// For local storage, returns the absolute file path (no presign needed).
func (c *localClient) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	return filepath.Join(c.basePath, normalizeKey(key)), nil
}

// GetPresignedGetURL implements Client.
// For local storage, returns the absolute file path (no presign needed).
func (c *localClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	return filepath.Join(c.basePath, normalizeKey(key)), nil
}

// GetPublicURL implements Client.
// For local storage, returns the absolute file path.
func (c *localClient) GetPublicURL(key string) string {
	return filepath.Join(c.basePath, normalizeKey(key))
}

// GetSize implements Client.
// Calculates total size of objects under a prefix.
func (c *localClient) GetSize(prefix string) (int64, error) {
	prefix = strings.TrimSpace(prefix)

	// If prefix is empty or is just a directory, calculate all files under it
	if prefix == "" || prefix == "." {
		var total int64
		err := filepath.Walk(c.basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		return total, err
	}

	// If prefix is a concrete file key, return its size
	fullPath := filepath.Join(c.basePath, prefix)
	info, err := os.Stat(fullPath)
	if err == nil {
		return info.Size(), nil
	}
	if !os.IsNotExist(err) {
		return 0, fmt.Errorf("gostorage local: stat file: %w", err)
	}

	// Prefix is a directory, calculate all files under it
	var total int64
	err = filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ListObjects implements Client.
// Lists all objects under a prefix.
func (c *localClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	prefix = strings.TrimSpace(prefix)

	walkPath := c.basePath
	if prefix != "" {
		walkPath = filepath.Join(walkPath, prefix)
	}

	err := filepath.Walk(walkPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Calculate relative key from base path
		relKey, err := filepath.Rel(c.basePath, path)
		if err != nil {
			return nil // Skip files we can't convert to key
		}

		// Normalize path separators
		relKey = strings.ReplaceAll(relKey, "\\", "/")

		res = append(res, ObjectInfo{
			Key:          relKey,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
		return nil
	})

	return res, err
}

// DeleteObject implements Client.
// Permanently removes the file at key.
func (c *localClient) DeleteObject(key string) error {
	path := filepath.Join(c.basePath, normalizeKey(key))

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // No-op for non-existent files
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("gostorage local: delete file: %w", err)
	}

	// Clean up empty parent directories
	cleanupEmptyDirs(c.basePath, filepath.Dir(path))

	return nil
}

// cleanupEmptyDirs removes empty parent directories recursively.
func cleanupEmptyDirs(basePath, dir string) {
	for {
		if dir == basePath || dir == "." || dir == "/" {
			break
		}
		// Check if directory is empty
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		// Remove empty directory
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}

// Type implements Client.
func (c *localClient) Type() string { return c.config.Type }

// Name implements Client.
func (c *localClient) Name() string { return c.config.Name }

// Kind implements Client.
func (c *localClient) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*localClient)(nil)
