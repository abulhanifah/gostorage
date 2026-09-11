package gostorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// gcsClient implements Client for Google Cloud Storage using the official
// cloud.google.com/go/storage SDK.
type gcsClient struct {
	client   *storage.Client
	bucket   *storage.BucketHandle
	config   Config
	endpoint string
	scheme   string
	// accessID and signKey are the GoogleAccessID and private key used to
	// compute V4 signed URLs locally, without holding credentials client-side.
	accessID string
	signKey  []byte
}

// serviceAccountKey is the subset of the Google service account key JSON
// needed to authenticate and sign URLs.
type serviceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// newGCS creates a Google Cloud Storage client.
//
// Credential sources, resolved in order:
//  1. cfg.SecretKey as a full Google service account key JSON: used to
//     authenticate the HTTP client and to derive the signing access id
//     (client_email) and private key.
//  2. cfg.SecretKey as a PEM-encoded private key; cfg.AccessKey holds the
//     service account client email. Client operations fall back to
//     Application Default Credentials.
//  3. Application Default Credentials only (no credentials configured).
//
// Signed URLs require a service account key either way; without one they fail
// with a descriptive error. A plain access key without a private key is
// rejected.
func newGCS(cfg Config) (Client, error) {
	var opts []option.ClientOption
	accessID := cfg.AccessKey
	var signKey []byte
	if secret := cfg.SecretKey; secret != "" {
		var sa *serviceAccountKey
		if json.Unmarshal([]byte(secret), &sa) == nil && sa != nil && sa.ClientEmail != "" && sa.PrivateKey != "" {
			opts = append(opts, option.WithCredentialsJSON([]byte(secret)))
			accessID = sa.ClientEmail
			signKey = []byte(sa.PrivateKey)
		} else {
			// PEM-encoded private key.
			if cfg.AccessKey == "" {
				return nil, fmt.Errorf("gostorage gcs: google access id (client email) required when a private key is provided")
			}
			signKey = []byte(secret)
		}
	}
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gostorage gcs: create client: %w", err)
	}
	scheme, endpoint := splitScheme(cfg.Endpoint)
	return &gcsClient{
		client:   client,
		bucket:   client.Bucket(cfg.Bucket),
		config:   cfg,
		endpoint: endpoint,
		scheme:   scheme,
		accessID: accessID,
		signKey:  signKey,
	}, nil
}

// GetPresignedUploadURL implements Client. Returns a V4 signed PUT URL
// computed locally from the service account private key.
func (c *gcsClient) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	opts := &storage.SignedURLOptions{
		Method:  "PUT",
		Scheme:  storage.SigningSchemeV4,
		Expires: time.Now().Add(expiry),
	}
	if contentType != "" {
		opts.ContentType = contentType
	}
	signed, err := c.sign(opts, key)
	if err != nil {
		return "", fmt.Errorf("gostorage gcs: presign upload: %w", err)
	}
	return signed, nil
}

// GetPresignedGetURL implements Client. Returns a V4 signed GET URL computed
// locally from the service account private key.
func (c *gcsClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	signed, err := c.sign(&storage.SignedURLOptions{
		Method:  "GET",
		Scheme:  storage.SigningSchemeV4,
		Expires: time.Now().Add(expiry),
	}, key)
	if err != nil {
		return "", fmt.Errorf("gostorage gcs: presign get: %w", err)
	}
	return signed, nil
}

// sign builds a V4 signed URL for the object at key. The signature is computed
// entirely locally from the service account key, so credentials never leave the
// server.
func (c *gcsClient) sign(opts *storage.SignedURLOptions, key string) (string, error) {
	if c.accessID == "" || len(c.signKey) == 0 {
		return "", fmt.Errorf("a Google service account key is required to sign URLs (set SecretKey to the service account key JSON)")
	}
	opts.GoogleAccessID = c.accessID
	opts.PrivateKey = c.signKey
	if c.endpoint != "" {
		opts.Hostname = c.endpoint
		opts.Insecure = c.scheme == "http"
	}
	return storage.SignedURL(c.config.Bucket, normalizeKey(key), opts)
}

// GetPublicURL implements Client. The Google Cloud Storage public layout is
// https://storage.googleapis.com/{bucket}/{key}.
func (c *gcsClient) GetPublicURL(key string) string {
	host := "storage.googleapis.com"
	if c.endpoint != "" && !strings.Contains(c.endpoint, "googleapis.com") {
		host = c.endpoint
	}
	return c.scheme + "://" + host + "/" + c.config.Bucket + "/" + normalizeKey(key)
}

// GetSize implements Client.
//
// GCS has no server-side operation that returns the aggregate size of a
// prefix; the only way to size a directory is to list its objects. As an
// optimization, when prefix is a concrete object key the size is read directly
// from the object metadata with a single call (ObjectHandle.Attrs) instead of
// listing it. The fast path is skipped for prefixes ending in "/" because such
// keys are usually zero-byte folder placeholders that also share the prefix
// with real objects.
func (c *gcsClient) GetSize(prefix string) (int64, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		if attrs, err := c.bucket.Object(prefix).Attrs(c.config.Context); err == nil {
			return attrs.Size, nil
		} else if !errors.Is(err, storage.ErrObjectNotExist) {
			return 0, fmt.Errorf("gostorage gcs: get object size: %w", err)
		}
	}
	var total int64
	objs, err := c.ListObjects(prefix)
	if err != nil {
		return total, err
	}
	for _, obj := range objs {
		total += obj.Size
	}
	return total, nil
}

// ListObjects implements Client.
func (c *gcsClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	it := c.bucket.Objects(c.config.Context, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			return res, nil
		}
		if err != nil {
			return res, fmt.Errorf("gostorage gcs: list objects: %w", err)
		}
		res = append(res, ObjectInfo{
			Key:          attrs.Name,
			Size:         attrs.Size,
			LastModified: attrs.Updated,
		})
	}
}

// DeleteObject implements Client.
func (c *gcsClient) DeleteObject(key string) error {
	if err := c.bucket.Object(normalizeKey(key)).Delete(c.config.Context); err != nil {
		return fmt.Errorf("gostorage gcs: delete object: %w", err)
	}
	return nil
}

// Type implements Client.
func (c *gcsClient) Type() string { return c.config.Type }

// Name implements Client.
func (c *gcsClient) Name() string { return c.config.Name }

// Kind implements Client.
func (c *gcsClient) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*gcsClient)(nil)
