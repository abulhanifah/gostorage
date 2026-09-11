// Package gostorage provides a unified, provider-agnostic client for object
// storage. It supports AWS S3, MinIO, Supabase Storage and Alibaba Cloud OSS
// through a single interface:
//
//   - presigned upload and download URLs (browser direct-to-storage uploads),
//   - public URLs,
//   - listing and sizing objects under a prefix (e.g. a sub-bucket),
//   - deleting objects.
//
// Credentials are never exposed to the client; only presigned or public URLs
// are returned.
package gostorage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Default timeouts and presign expiries used when the caller does not provide
// its own values.
const (
	// DefaultTimeout is the client-side HTTP timeout used for provider
	// REST/raw HTTP calls such as OSS listing or deletion.
	DefaultTimeout = 30 * time.Second

	// DefaultPresignUploadExpiry is the default lifetime of a presigned
	// upload URL.
	DefaultPresignUploadExpiry = 15 * time.Minute

	// DefaultPresignGetExpiry is the default lifetime of a presigned
	// download URL.
	DefaultPresignGetExpiry = 15 * time.Minute
)

// Kind identifies a supported storage provider.
type Kind string

const (
	// KindS3 is Amazon Web Services S3 or any other S3-compatible endpoint
	// using virtual-hosted style URLs (e.g. Cloudflare R2, DigitalOcean
	// Spaces).
	KindS3 Kind = "s3"

	// KindMinio is MinIO, typically reachable over a raw host:port
	// endpoint using path-style URLs.
	KindMinio Kind = "minio"

	// KindSupabase is Supabase Storage, accessed through its REST API.
	KindSupabase Kind = "supabase"

	// KindOSS is Alibaba Cloud Object Storage Service.
	KindOSS Kind = "oss"
)

// knownKinds is the canonical set of supported providers.
var knownKinds = []Kind{KindS3, KindMinio, KindSupabase, KindOSS}

// SupportedKinds returns all storage kinds supported by this library.
func SupportedKinds() []Kind {
	out := make([]Kind, len(knownKinds))
	copy(out, knownKinds)
	return out
}

// Config describes a single storage backend. Kind is derived from Type when
// empty and Bucket defaults to Name.
type Config struct {
	// Type is the fully qualified type such as "acme-s3".
	// The qualifier before the first "-" is arbitrary and is stripped when
	// Kind is not set; a plain kind such as "s3" is also accepted.
	Type string

	// Kind is the raw provider kind: one of KindS3, KindMinio, KindSupabase
	// or KindOSS. Derived from Type when empty.
	Kind Kind

	// Name is the user-facing storage name (e.g. "chum-bucket").
	Name string

	// Bucket is the bucket inside the provider. Defaults to Name when empty.
	Bucket string

	// Endpoint is the provider endpoint. It may include a scheme
	// (http:// or https://); otherwise TLS is assumed.
	Endpoint string

	// Region is the provider region. For Supabase it holds the project
	// reference id that maps to "<ref>.supabase.co".
	Region string

	// AccessKey is the provider access key. For Supabase it is the anon or
	// service_role API key used as the Bearer token.
	AccessKey string

	// SecretKey is the provider secret key.
	SecretKey string

	// Context is the base context used for storage operations. Defaults to
	// context.Background() when nil.
	Context context.Context

	// Timeout bounds HTTP requests made directly by this library (OSS raw
	// calls). Defaults to DefaultTimeout.
	Timeout time.Duration
}

// ParseType splits a storage type into its provider qualifier and raw kind.
//
// The type is split on the first "-": anything before it is treated as an
// arbitrary provider qualifier and the remainder must be a supported kind
// (e.g. "acme-s3", "acme-oss", "acme-minio"). A bare kind such as "s3" is
// returned with an empty qualifier. Parsing is case insensitive.
func ParseType(tpe string) (qualifier string, kind Kind) {
	tpe = strings.ToLower(strings.TrimSpace(tpe))
	parts := strings.SplitN(tpe, "-", 2)
	if len(parts) != 2 {
		return "", Kind(tpe)
	}
	return parts[0], Kind(parts[1])
}

// normalize validates the configuration and derives Kind and Bucket when they
// are not explicitly set.
func (c Config) normalize() (Config, error) {
	c.Type = strings.TrimSpace(c.Type)
	c.Kind = Kind(strings.ToLower(strings.TrimSpace(string(c.Kind))))
	if c.Kind == "" {
		_, kind := ParseType(c.Type)
		c.Kind = kind
	}
	c.Endpoint = strings.TrimSpace(strings.TrimSuffix(c.Endpoint, "/"))
	c.Bucket = strings.TrimSpace(c.Bucket)
	if c.Bucket == "" {
		c.Bucket = strings.TrimSpace(c.Name)
	}
	if c.Context == nil {
		c.Context = context.Background()
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if !c.isKnownKind() {
		return c, fmt.Errorf("gostorage: unsupported storage kind %q", c.Kind)
	}
	return c, nil
}

// isKnownKind reports whether c.Kind is one of the supported providers.
func (c Config) isKnownKind() bool {
	for _, k := range knownKinds {
		if c.Kind == k {
			return true
		}
	}
	return false
}

// Client is the unified interface implemented by every storage provider.
//
// Every URL produced by this interface is self-contained: the signature or
// public access is embedded in it, so any HTTP client (such as a browser) can
// upload or download objects without ever holding the provider credentials.
// All methods are safe for concurrent use by multiple goroutines.
type Client interface {
	// GetPresignedUploadURL returns a URL that lets a client upload the
	// object at key without holding credentials, using an HTTP PUT request.
	//
	// For S3, MinIO and OSS a signed PUT URL for bucket/key is returned.
	// contentType, when non-empty, is bound into the signature so the
	// provider rejects uploads with a mismatched Content-Type; expiry is
	// honored with one-second precision.
	//
	// For Supabase a server-side signed upload URL is returned instead.
	// contentType is not part of the Supabase contract and its lifetime is
	// governed by the provider, so expiry is ignored; keep the default
	// (DefaultPresignUploadExpiry) so the browser has enough time to
	// complete the PUT.
	//
	// Returns an error only when the provider rejects the signing
	// operation, e.g. because the credentials are invalid.
	GetPresignedUploadURL(key, contentType string, expiry time.Duration) (string, error)

	// GetPresignedGetURL returns a signed URL that lets a client download
	// the object at key without holding credentials.
	//
	// For S3, MinIO and OSS the returned URL is an HTTP GET request for
	// bucket/key. For Supabase a signed download URL is returned instead;
	// expiry is honored at one-second precision.
	//
	// Returns an error only when the provider rejects the signing
	// operation.
	GetPresignedGetURL(key string, expiry time.Duration) (string, error)

	// GetPublicURL returns the public (unsigned) URL of the object at key.
	//
	// The object must be publicly readable through the provider's bucket
	// policy; no signing or credentials are involved and this method never
	// returns an error. An empty key yields a URL ending with the bucket
	// path.
	GetPublicURL(key string) string

	// GetSize returns the total size in bytes of every object stored under
	// prefix. Prefixes that expand to nothing (or to only empty objects)
	// return zero. The error is non-nil only when listing the provider
	// fails, in which case the returned size may be incomplete.
	GetSize(prefix string) (int64, error)

	// ListObjects returns the objects stored under prefix, listed
	// recursively. Keys are relative to the bucket and their order is
	// unspecified. The error is non-nil only when listing the provider
	// fails, in which case the returned slice may be incomplete.
	ListObjects(prefix string) ([]ObjectInfo, error)

	// DeleteObject permanently removes the object at key. Providers treat
	// deleting an already-missing object as a no-op or an error depending
	// on their semantics; the error is non-nil only when the deletion could
	// not be performed.
	DeleteObject(key string) error

	// Type returns the full storage type as configured, e.g. "acme-s3".
	Type() string

	// Name returns the logical storage name as configured.
	Name() string

	// Kind returns the resolved provider kind.
	Kind() Kind
}

// ObjectInfo describes an object stored inside a bucket.
type ObjectInfo struct {
	// Key is the object key, relative to the bucket.
	Key string

	// Size is the object size in bytes.
	Size int64

	// LastModified is the object modification time when known.
	LastModified time.Time
}

// New builds a Client for the given config, routing to the provider matching
// the resolved Kind.
func New(cfg Config) (Client, error) {
	c, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case KindS3:
		return newS3(c)
	case KindMinio:
		return newMinio(c)
	case KindSupabase:
		return newSupabase(c)
	case KindOSS:
		return newOSS(c)
	default:
		// Unreachable: normalize already validated the kind.
		return nil, fmt.Errorf("gostorage: unsupported storage kind %q", c.Kind)
	}
}

// NewS3 builds an AWS S3 (or S3-compatible) client. cfg.Kind is forced to
// KindS3.
func NewS3(cfg Config) (Client, error) {
	cfg.Kind = KindS3
	return New(cfg)
}

// NewMinio builds a MinIO client. cfg.Kind is forced to KindMinio.
func NewMinio(cfg Config) (Client, error) {
	cfg.Kind = KindMinio
	return New(cfg)
}

// NewSupabase builds a Supabase Storage client. cfg.Kind is forced to
// KindSupabase.
func NewSupabase(cfg Config) (Client, error) {
	cfg.Kind = KindSupabase
	return New(cfg)
}

// NewOSS builds an Alibaba Cloud OSS client. cfg.Kind is forced to KindOSS.
func NewOSS(cfg Config) (Client, error) {
	cfg.Kind = KindOSS
	return New(cfg)
}

// splitScheme extracts the scheme and host/endpoint without a trailing slash
// from an endpoint value. "https://" is assumed when no scheme is present.
// Returns the scheme, the cleaned endpoint and whether the endpoint carries a
// non-default (http) scheme.
func splitScheme(endpoint string) (scheme, host string) {
	ep := strings.TrimSuffix(endpoint, "/")
	scheme = "https"
	switch {
	case strings.HasPrefix(ep, "http://"):
		scheme = "http"
		ep = strings.TrimPrefix(ep, "http://")
	case strings.HasPrefix(ep, "https://"):
		ep = strings.TrimPrefix(ep, "https://")
	}
	return scheme, ep
}

// normalizeKey trims a leading slash and disallows empty keys where the
// underlying provider requires a path segment.
func normalizeKey(key string) string {
	return strings.TrimPrefix(strings.TrimSpace(key), "/")
}
