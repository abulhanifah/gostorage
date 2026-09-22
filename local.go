package gostorage

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// LocalSign signs a local filesystem object URL with an HMAC-SHA256 digest.
//
// Secret, method, key, expires and contentType are all bound into the
// signature so that a signature issued for one operation cannot be reused for
// another (including a PUT upload vs a GET download) or after its expiry has
// passed. The key is normalized exactly as it is stored, and contentType is
// normalized (trimmed and lower-cased), so the exact same arguments must be
// fed to LocalVerify or to the local GetPresignedUploadURL/GetPresignedGetURL
// implementations.
func LocalSign(secret, method, key string, expires int64, contentType string) string {
	h := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(h, "%s\n%s\n%d\n%s", strings.ToUpper(method), normalizeKey(key), expires, normalizeContentType(contentType))
	return hex.EncodeToString(h.Sum(nil))
}

// LocalVerify reports whether signature is a valid LocalSign digest for the
// given secret, method, key, expires and contentType. The comparison is
// constant-time so that signature values cannot be guessed by timing.
//
// Expiry is intentionally not enforced here; it is the caller's job to decide
// how to treat a signature whose expires timestamp is already in the past.
func LocalVerify(secret, method, key string, expires int64, contentType, signature string) bool {
	want := LocalSign(secret, method, key, expires, contentType)
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(signature))) == 1
}

// normalizeContentType normalizes a Content-Type value the same way on both
// the signing and the verification side so the two always agree.
func normalizeContentType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(contentType))
}

// presignURL renders the URL for key for either a PUT (upload) or GET
// (download) operation, given the operation already decided by the caller.
func (c *localClient) presignURL(method, key, contentType string, expiry time.Duration) (string, error) {
	key = normalizeKey(key)

	// Legacy behavior (no public base): the object is addressed by its
	// absolute on-disk path.
	if c.config.PublicBaseURL == "" {
		return filepath.Join(c.basePath, key), nil
	}

	expires := time.Now().Add(expiry).Unix()
	sig := LocalSign(c.config.SecretKey, method, key, expires, contentType)
	return fmt.Sprintf("%s/%s?expires=%d&sig=%s",
		strings.TrimSuffix(c.config.PublicBaseURL, "/"), key, expires, sig), nil
}

// GetPresignedUploadURL implements Client.
// For local storage, returns a signed PUT URL into Config.PublicBaseURL
// (contentType is bound into the signature), or the absolute file path when
// no public base is configured.
func (c *localClient) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	return c.presignURL("PUT", key, contentType, expiry)
}

// GetPresignedGetURL implements Client.
// For local storage, returns a signed GET URL into Config.PublicBaseURL, or
// the absolute file path when no public base is configured.
func (c *localClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	return c.presignURL("GET", key, "", expiry)
}

// GetPublicURL implements Client.
// For local storage, returns the unsigned URL under Config.PublicBaseURL, or
// the absolute file path when no public base is configured.
func (c *localClient) GetPublicURL(key string) string {
	key = normalizeKey(key)
	if c.config.PublicBaseURL == "" {
		return filepath.Join(c.basePath, key)
	}
	return strings.TrimSuffix(c.config.PublicBaseURL, "/") + "/" + key
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
		if !info.IsDir() {
			return info.Size(), nil
		}
	} else if os.IsNotExist(err) {
		// nothing is stored under the prefix
		return 0, nil
	} else {
		return 0, fmt.Errorf("gostorage local: stat path: %w", err)
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
	if os.IsNotExist(err) {
		// nothing is stored under the prefix
		return res, nil
	}

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
