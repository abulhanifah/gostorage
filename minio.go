package gostorage

import (
	"fmt"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// newMinioSDKClient builds the underlying minio-go client (MinIO's own
// official SDK) and returns the normalized scheme + endpoint used to build
// public URLs.
func newMinioSDKClient(cfg Config) (*minio.Client, string, string, error) {
	scheme, endpoint := splitScheme(cfg.Endpoint)

	region := cfg.Region
	if region == "" {
		// MinIO ignores the region value, but setting one skips minio-go's
		// network bucket-location probe during presigning, keeping presign
		// and GetPublicURL fully local.
		region = "us-east-1"
	}

	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: scheme == "https",
		Region: region,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("gostorage minio: %w", err)
	}
	return cl, scheme, endpoint, nil
}

// minioClient implements Client for MinIO using minio-go, MinIO's own official
// Go SDK. Object visibility is path-style because self-hosted MinIO
// deployments usually do not expose a bucket DNS name.
type minioClient struct {
	client   *minio.Client
	config   Config
	endpoint string
	scheme   string
}

// newMinio creates an independent MinIO client.
func newMinio(cfg Config) (Client, error) {
	cl, scheme, host, err := newMinioSDKClient(cfg)
	if err != nil {
		return nil, err
	}
	return &minioClient{
		client:   cl,
		config:   cfg,
		endpoint: host,
		scheme:   scheme,
	}, nil
}

// GetPresignedUploadURL implements Client.
func (c *minioClient) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	var extraHeaders http.Header
	if contentType != "" {
		extraHeaders = http.Header{}
		extraHeaders.Set("Content-Type", contentType)
	}
	u, err := c.client.PresignHeader(c.config.Context, http.MethodPut, c.config.Bucket, normalizeKey(key), expiry, nil, extraHeaders)
	if err != nil {
		return "", fmt.Errorf("gostorage minio: presign upload: %w", err)
	}
	return u.String(), nil
}

// GetPresignedGetURL implements Client.
func (c *minioClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	u, err := c.client.PresignedGetObject(c.config.Context, c.config.Bucket, normalizeKey(key), expiry, nil)
	if err != nil {
		return "", fmt.Errorf("gostorage minio: presign get: %w", err)
	}
	return u.String(), nil
}

// GetPublicURL implements Client. MinIO objects are exposed with the
// path-style layout https://{endpoint}/{bucket}/{key}.
func (c *minioClient) GetPublicURL(key string) string {
	return c.scheme + "://" + c.endpoint + "/" + c.config.Bucket + "/" + normalizeKey(key)
}

// GetSize implements Client.
func (c *minioClient) GetSize(prefix string) (int64, error) {
	var total int64
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: true}
	for obj := range c.client.ListObjects(c.config.Context, c.config.Bucket, opts) {
		if obj.Err != nil {
			return total, fmt.Errorf("gostorage minio: list objects: %w", obj.Err)
		}
		total += obj.Size
	}
	return total, nil
}

// ListObjects implements Client.
func (c *minioClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: true}
	for obj := range c.client.ListObjects(c.config.Context, c.config.Bucket, opts) {
		if obj.Err != nil {
			return res, fmt.Errorf("gostorage minio: list objects: %w", obj.Err)
		}
		res = append(res, ObjectInfo{Key: obj.Key, Size: obj.Size, LastModified: obj.LastModified})
	}
	return res, nil
}

// DeleteObject implements Client.
func (c *minioClient) DeleteObject(key string) error {
	err := c.client.RemoveObject(c.config.Context, c.config.Bucket, normalizeKey(key), minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("gostorage minio: delete object: %w", err)
	}
	return nil
}

// Type implements Client.
func (c *minioClient) Type() string { return c.config.Type }

// Name implements Client.
func (c *minioClient) Name() string { return c.config.Name }

// Kind implements Client.
func (c *minioClient) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*minioClient)(nil)
