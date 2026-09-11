package gostorage

import (
	"fmt"
	"strings"
	"time"

	storage_go "github.com/supabase-community/storage-go"
)

// supabaseClient implements Client for Supabase Storage through its REST API
// using the official supabase-community/storage-go library. The configured
// AccessKey is sent as a Bearer token (anon or service_role key).
type supabaseClient struct {
	client  *storage_go.Client
	config  Config
	bucket  string
	host    string
	baseURL string
}

// newSupabase creates a Supabase Storage client. The endpoint is expected to
// be "https://storage.{ref}.supabase.co" (or any host); the trailing
// "storage." subdomain is normalized away so REST and public URLs target
// "https://{ref}.supabase.co/storage/v1".
func newSupabase(cfg Config) (Client, error) {
	host := supabaseHost(cfg.Endpoint)
	baseURL := "https://" + host + "/storage/v1"
	c := &supabaseClient{
		client:  storage_go.NewClient(baseURL, cfg.AccessKey, nil),
		config:  cfg,
		bucket:  cfg.Bucket,
		host:    host,
		baseURL: baseURL,
	}
	return c, nil
}

// supabaseHost returns the project host without scheme and without the
// "storage." subdomain, e.g. "https://storage.abcd1234.supabase.co" ->
// "abcd1234.supabase.co".
func supabaseHost(endpoint string) string {
	host := strings.TrimPrefix(endpoint, "http://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "storage.")
	return strings.TrimSuffix(host, "/")
}

// GetPresignedUploadURL implements Client. Supabase issues a signed URL that is
// uploaded to with an HTTP PUT.
func (c *supabaseClient) GetPresignedUploadURL(key, _ string, _ time.Duration) (string, error) {
	resp, err := c.client.CreateSignedUploadUrl(c.bucket, normalizeKey(key))
	if err != nil {
		return "", fmt.Errorf("gostorage supabase: presign upload: %w", err)
	}
	if strings.HasPrefix(resp.Url, "http") {
		return resp.Url, nil
	}
	return c.baseURL + "/" + strings.TrimPrefix(resp.Url, "/"), nil
}

// GetPresignedGetURL implements Client.
func (c *supabaseClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	resp, err := c.client.CreateSignedUrl(c.bucket, normalizeKey(key), int(expiry.Seconds()))
	if err != nil {
		return "", fmt.Errorf("gostorage supabase: presign get: %w", err)
	}
	return resp.SignedURL, nil
}

// GetPublicURL implements Client.
func (c *supabaseClient) GetPublicURL(key string) string {
	return "https://" + c.host + "/storage/v1/object/public/" + c.bucket + "/" + normalizeKey(key)
}

// listFiles paginates through every object under prefix.
func (c *supabaseClient) listFiles(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	offset := 0
	for {
		objects, err := c.client.ListFiles(c.bucket, prefix, storage_go.FileSearchOptions{
			Limit:  1000,
			Offset: offset,
		})
		if err != nil {
			return res, fmt.Errorf("gostorage supabase: list files: %w", err)
		}
		for _, obj := range objects {
			lastModified, _ := time.Parse(time.RFC3339, obj.UpdatedAt)
			res = append(res, ObjectInfo{
				Key:          obj.Name,
				Size:         supabaseObjectSize(obj.Metadata),
				LastModified: lastModified,
			})
		}
		if len(objects) == 0 {
			break
		}
		offset += len(objects)
		if len(objects) < 1000 {
			break
		}
	}
	return res, nil
}

// supabaseObjectSize extracts the object size from the opaque metadata map
// returned by Supabase. Unknown shapes are treated as zero size.
func supabaseObjectSize(metadata any) int64 {
	m, ok := metadata.(map[string]any)
	if !ok {
		return 0
	}
	switch v := m["size"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// GetSize implements Client.
func (c *supabaseClient) GetSize(prefix string) (int64, error) {
	objects, err := c.listFiles(prefix)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, obj := range objects {
		total += obj.Size
	}
	return total, nil
}

// ListObjects implements Client.
func (c *supabaseClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	return c.listFiles(prefix)
}

// DeleteObject implements Client.
func (c *supabaseClient) DeleteObject(key string) error {
	_, err := c.client.RemoveFile(c.bucket, []string{normalizeKey(key)})
	if err != nil {
		return fmt.Errorf("gostorage supabase: delete object: %w", err)
	}
	return nil
}

// Type implements Client.
func (c *supabaseClient) Type() string { return c.config.Type }

// Name implements Client.
func (c *supabaseClient) Name() string { return c.config.Name }

// Kind implements Client.
func (c *supabaseClient) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*supabaseClient)(nil)
