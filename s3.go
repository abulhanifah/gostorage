package gostorage

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3Client implements Client for AWS S3 using the official AWS SDK for Go v2
// (github.com/aws/aws-sdk-go-v2/service/s3). Object visibility is
// virtual-hosted style. Presigning is done through the S3 PresignClient, which
// computes signatures locally without network I/O.
type s3Client struct {
	client   *s3.Client
	presign  *s3.PresignClient
	config   Config
	endpoint string
	scheme   string
}

// newS3 creates an AWS S3 client. The bucket must be reachable over a bucket
// DNS name, hence URLs are built with the virtual-hosted layout.
func newS3(cfg Config) (Client, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}
	scheme, endpoint := splitScheme(cfg.Endpoint)
	svc := s3.NewFromConfig(awsCfg)
	pc := s3.NewPresignClient(svc)
	return &s3Client{
		client:   svc,
		presign:  pc,
		config:   cfg,
		endpoint: endpoint,
		scheme:   scheme,
	}, nil
}

// GetPresignedUploadURL implements Client.
func (c *s3Client) GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(normalizeKey(key)),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	resp, err := c.presign.PresignPutObject(c.config.Context, input, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("gostorage s3: presign upload: %w", err)
	}
	return resp.URL, nil
}

// GetPresignedGetURL implements Client.
func (c *s3Client) GetPresignedGetURL(key string, expiry time.Duration) (string, error) {
	resp, err := c.presign.PresignGetObject(c.config.Context, &s3.GetObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(normalizeKey(key)),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("gostorage s3: presign get: %w", err)
	}
	return resp.URL, nil
}

// GetPublicURL implements Client.
func (c *s3Client) GetPublicURL(key string) string {
	bucket := c.config.Bucket
	if strings.Contains(c.endpoint, "amazonaws.com") {
		if c.config.Region != "" {
			return c.scheme + "://" + bucket + ".s3." + c.config.Region + ".amazonaws.com/" + normalizeKey(key)
		}
		return c.scheme + "://" + bucket + ".s3.amazonaws.com/" + normalizeKey(key)
	}
	return c.scheme + "://" + bucket + "." + c.endpoint + "/" + normalizeKey(key)
}

// GetSize implements Client.
//
// S3 has no server-side operation that returns the aggregate size of a
// prefix; the only way to size a directory is to list its objects. As an
// optimization, when prefix is a concrete object key the size is read directly
// from the object metadata with a single call (HeadObject) instead of listing
// it. The fast path is skipped for prefixes ending in "/" because such keys
// are usually zero-byte folder placeholders that also share the prefix with
// real objects.
func (c *s3Client) GetSize(prefix string) (int64, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		out, err := c.client.HeadObject(c.config.Context, &s3.HeadObjectInput{
			Bucket: aws.String(c.config.Bucket),
			Key:    aws.String(prefix),
		})
		if err == nil {
			return aws.ToInt64(out.ContentLength), nil
		}
		var nf *types.NotFound
		if !errors.As(err, &nf) {
			return 0, fmt.Errorf("gostorage s3: head object: %w", err)
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
func (c *s3Client) ListObjects(prefix string) ([]ObjectInfo, error) {
	res := []ObjectInfo{}
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(c.config.Bucket),
		Prefix: aws.String(prefix),
	}
	for {
		out, err := c.client.ListObjectsV2(c.config.Context, input)
		if err != nil {
			return res, fmt.Errorf("gostorage s3: list objects: %w", err)
		}
		for _, obj := range out.Contents {
			res = append(res, ObjectInfo{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: deref(obj.LastModified),
			})
		}
		if !aws.ToBool(out.IsTruncated) {
			return res, nil
		}
		input.ContinuationToken = out.NextContinuationToken
	}
}

// DeleteObject implements Client.
func (c *s3Client) DeleteObject(key string) error {
	_, err := c.client.DeleteObject(c.config.Context, &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(normalizeKey(key)),
	})
	if err != nil {
		return fmt.Errorf("gostorage s3: delete object: %w", err)
	}
	return nil
}

// Type implements Client.
func (c *s3Client) Type() string { return c.config.Type }

// Name implements Client.
func (c *s3Client) Name() string { return c.config.Name }

// Kind implements Client.
func (c *s3Client) Kind() Kind { return c.config.Kind }

// compile-time interface guard.
var _ Client = (*s3Client)(nil)
