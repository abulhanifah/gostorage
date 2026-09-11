package gostorage

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

// ossClient implements Client for Alibaba Cloud OSS using the official
// github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss SDK. Object visibility is
// virtual-hosted style.
type ossClient struct {
	client   *oss.Client
	config   Config
	endpoint string
	scheme   string
}

// newOSS creates an Alibaba Cloud OSS client.
func newOSS(cfg Config) (Client, error) {
	ossCfg := &oss.Config{
		Endpoint:            oss.Ptr(cfg.Endpoint),
		CredentialsProvider: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey),
	}
	client := oss.NewClient(ossCfg)
	scheme, endpoint := splitScheme(cfg.Endpoint)
	return &ossClient{
		client:   client,
		config:   cfg,
		endpoint: endpoint,
		scheme:   scheme,
	}, nil
}

// GetPresignedUploadURL implements Client.
func (c *ossClient) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	req := &oss.PutObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket),
		Key:    oss.Ptr(normalizeKey(key)),
	}
	if contentType != "" {
		req.ContentType = oss.Ptr(contentType)
	}
	res, err := c.client.Presign(c.config.Context, req, oss.PresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("gostorage oss: presign upload: %w", err)
	}
	return res.URL, nil
}

// GetPresignedGetURL implements Client.
func (c *ossClient) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	res, err := c.client.Presign(c.config.Context, &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket),
		Key:    oss.Ptr(normalizeKey(key)),
	}, oss.PresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("gostorage oss: presign get: %w", err)
	}
	return res.URL, nil
}

// GetPublicURL implements Client. OSS objects are exposed with the
// virtual-hosted layout https://{bucket}.{endpoint}/{key}.
func (c *ossClient) GetPublicURL(key string) string {
	return c.scheme + "://" + c.config.Bucket + "." + c.endpoint + "/" + normalizeKey(key)
}

// GetSize implements Client.
//
// OSS has no server-side operation that returns the aggregate size of a
// prefix; the only way to size a directory is to list its objects. As an
// optimization, when prefix is a concrete object key the size is read directly
// from the object metadata with a single call (HeadObject) instead of listing
// it. The fast path is skipped for prefixes ending in "/" because such keys
// are usually zero-byte folder placeholders that also share the prefix with
// real objects.
func (c *ossClient) GetSize(prefix string) (int64, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		res, err := c.client.HeadObject(c.config.Context, &oss.HeadObjectRequest{
			Bucket: oss.Ptr(c.config.Bucket),
			Key:    oss.Ptr(prefix),
		})
		if err == nil {
			return res.ContentLength, nil
		}
		var se *oss.ServiceError
		if !errors.As(err, &se) || se.StatusCode != http.StatusNotFound {
			return 0, fmt.Errorf("gostorage oss: head object: %w", err)
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
func (c *ossClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	req := &oss.ListObjectsV2Request{
		Bucket: oss.Ptr(c.config.Bucket),
		Prefix: oss.Ptr(prefix),
	}
	for {
		out, err := c.client.ListObjectsV2(c.config.Context, req)
		if err != nil {
			return res, fmt.Errorf("gostorage oss: list objects: %w", err)
		}
		for i := range out.Contents {
			obj := &out.Contents[i]
			res = append(res, ObjectInfo{
				Key:          deref(obj.Key),
				Size:         obj.Size,
				LastModified: deref(obj.LastModified),
			})
		}
		if !out.IsTruncated {
			return res, nil
		}
		req.ContinuationToken = out.NextContinuationToken
	}
}

// DeleteObject implements Client.
func (c *ossClient) DeleteObject(key string) error {
	_, err := c.client.DeleteObject(c.config.Context, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket),
		Key:    oss.Ptr(normalizeKey(key)),
	})
	if err != nil {
		return fmt.Errorf("gostorage oss: delete object: %w", err)
	}
	return nil
}

// Type implements Client.
func (c *ossClient) Type() string { return c.config.Type }

// Name implements Client.
func (c *ossClient) Name() string { return c.config.Name }

// Kind implements Client.
func (c *ossClient) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*ossClient)(nil)

// deref returns the value behind p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
