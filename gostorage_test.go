package gostorage

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gcsTestKey returns a syntactically valid Google service account key JSON
// with a freshly generated RSA private key, so GCS clients and V4 signed URLs
// can be exercised without network access.
func gcsTestKey(t *testing.T) []byte {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	b, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "test-key-id",
		"client_email":   "storage-test@test-project.iam.gserviceaccount.com",
		"client_id":      "1234567890",
		"private_key":    string(keyPEM),
	})
	if err != nil {
		t.Fatalf("marshal service account key: %v", err)
	}
	return b
}

func TestParseType(t *testing.T) {
	tests := []struct {
		in       string
		provider string
		kind     Kind
	}{
		{"zahir-s3", "zahir", KindS3},
		{"private-oss", "private", KindOSS},
		{"private-minio", "private", KindMinio},
		{"zahir-supabase", "zahir", KindSupabase},
		{"zahir-gcs", "zahir", KindGCS},
		{"acme-s3", "acme", KindS3},
		{"s3", "", KindS3},
		{"oss", "", KindOSS},
		{"local", "", Kind("local")},
		{"ZAHIR-S3", "zahir", KindS3},
		{"acme-nosql", "acme", Kind("nosql")},
	}
	for _, tt := range tests {
		p, k := ParseType(tt.in)
		if p != tt.provider || k != tt.kind {
			t.Errorf("ParseType(%q) = (%q, %q), want (%q, %q)", tt.in, p, k, tt.provider, tt.kind)
		}
	}
}

func TestNormalize(t *testing.T) {
	c, err := (Config{Type: "zahir-s3", Name: "attachments"}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.Kind != KindS3 {
		t.Errorf("Kind = %q, want s3", c.Kind)
	}
	if c.Bucket != c.Name {
		t.Errorf("Bucket = %q, want derived from Name %q", c.Bucket, c.Name)
	}
	if c.Context == nil {
		t.Error("Context should default to background")
	}

	c, err = (Config{Type: "zahir-minio", Name: "m", Bucket: "files"}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.Kind != KindMinio {
		t.Errorf("Kind = %q, want minio", c.Kind)
	}
	if c.Bucket != "files" {
		t.Errorf("Bucket = %q, want explicit files", c.Bucket)
	}

	c, err = (Config{Type: "zahir-local", Name: "local-store", Endpoint: "/tmp/storage"}).normalize()
	if err != nil {
		t.Fatalf("normalize should accept local kind: %v", err)
	}
	if c.Kind != KindLocal {
		t.Errorf("Kind = %q, want local", c.Kind)
	}
}

func TestNewFactory(t *testing.T) {
	base := Config{Name: "x", Endpoint: "s3.amazonaws.com"}
	if c, err := NewS3(base); err != nil {
		t.Fatalf("NewS3: %v", err)
	} else if _, ok := c.(*s3Client); !ok {
		t.Errorf("NewS3 returned %T, want *s3Client", c)
	}
	if c, err := NewMinio(base); err != nil {
		t.Fatalf("NewMinio: %v", err)
	} else if _, ok := c.(*minioClient); !ok {
		t.Errorf("NewMinio returned %T, want *minioClient", c)
	}
	if c, err := NewSupabase(base); err != nil {
		t.Fatalf("NewSupabase: %v", err)
	} else if _, ok := c.(*supabaseClient); !ok {
		t.Errorf("NewSupabase returned %T, want *supabaseClient", c)
	}
	if c, err := NewOSS(base); err != nil {
		t.Fatalf("NewOSS: %v", err)
	} else if _, ok := c.(*ossClient); !ok {
		t.Errorf("NewOSS returned %T, want *ossClient", c)
	}
	gcs := Config{Name: "x", Endpoint: "storage.googleapis.com", SecretKey: string(gcsTestKey(t))}
	if c, err := NewGCS(gcs); err != nil {
		t.Fatalf("NewGCS: %v", err)
	} else if _, ok := c.(*gcsClient); !ok {
		t.Errorf("NewGCS returned %T, want *gcsClient", c)
	}

	local := Config{Name: "local-test", Endpoint: "/tmp/gostorage-test"}
	if c, err := NewLocal(local); err != nil {
		t.Fatalf("NewLocal: %v", err)
	} else if _, ok := c.(*localClient); !ok {
		t.Errorf("NewLocal returned %T, want *localClient", c)
	}

	if _, err := New(Config{Type: "zahir-ftp"}); err == nil {
		t.Error("New should reject unknown kind")
	}
}

// TestLocalFilesystem exercises the local filesystem client end to end: URLs
// resolve to file paths and missing prefixes size zero and list no objects
// instead of erroring.
func TestLocalFilesystem(t *testing.T) {
	dir := t.TempDir()
	client, err := NewLocal(Config{
		Type:     "zahir-local",
		Name:     "local-store",
		Bucket:   "local-store",
		Endpoint: dir,
	})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	// missing prefix: nothing stored, zero size, no objects, no error
	if size, err := client.GetSize("does-not-exist"); err != nil {
		t.Errorf("GetSize(missing prefix) = %v, want nil", err)
	} else if size != 0 {
		t.Errorf("GetSize(missing prefix) = %d, want 0", size)
	}
	if objs, err := client.ListObjects("does-not-exist"); err != nil {
		t.Errorf("ListObjects(missing prefix) = %v, want nil", err)
	} else if len(objs) != 0 {
		t.Errorf("ListObjects(missing prefix) = %v, want empty", objs)
	}

	// write a file under a sub-bucket prefix
	key := "company-a/reports/ann.pdf"
	full := filepath.Join(dir, key)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte("hello world"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// the file key resolves to the absolute filesystem path
	got, err := client.GetPresignedUploadURL(key, "application/pdf", time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedUploadURL: %v", err)
	}
	if want := full; got != want {
		t.Errorf("GetPresignedUploadURL = %q, want %q", got, want)
	}
	if got := client.GetPublicURL(key); got != full {
		t.Errorf("GetPublicURL = %q, want %q", got, full)
	}

	// exact-file GetSize returns its size
	if size, err := client.GetSize(key); err != nil {
		t.Errorf("GetSize(file) = %v, want nil", err)
	} else if size != 11 {
		t.Errorf("GetSize(file) = %d, want 11", size)
	}

	// prefix GetSize sums the directory
	if size, err := client.GetSize("company-a"); err != nil {
		t.Errorf("GetSize(dir) = %v, want nil", err)
	} else if size != 11 {
		t.Errorf("GetSize(dir) = %d, want 11", size)
	}

	// ListObjects returns keys relative to the base path
	objs, err := client.ListObjects("company-a")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 1 || objs[0].Key != key || objs[0].Size != 11 {
		t.Errorf("ListObjects = %#v, want single %q object of size 11", objs, key)
	}

	if err := client.DeleteObject(key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if size, err := client.GetSize("company-a"); err != nil {
		t.Errorf("GetSize after delete = %v, want nil", err)
	} else if size != 0 {
		t.Errorf("GetSize after delete = %d, want 0", size)
	}
}

// TestLocalPresign verifies the local provider presigns upload and download
// URLs into Config.PublicBaseURL when it is set, binding method, key, expiry
// and content type into the HMAC signature.
func TestLocalPresign(t *testing.T) {
	const secret = "s3cret"
	base := "https://files.example.com/storages"
	dir := t.TempDir()
	client, err := NewLocal(Config{
		Type:          "zahir-local",
		Name:          "local-store",
		Endpoint:      dir,
		SecretKey:     secret,
		PublicBaseURL: base,
	})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	key := "company-a/reports/ann.pdf"

	// public url is unsigned, under the base, with a single slash regardless
	// of a trailing slash on the base
	pub := client.GetPublicURL(key)
	if pub != base+"/"+key {
		t.Errorf("GetPublicURL = %q, want %q", pub, base+"/"+key)
	}

	// upload url carries expires+sig and the signature is bound to the PUT
	// method and the content type
	upload, err := client.GetPresignedUploadURL(key, "application/pdf", time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedUploadURL: %v", err)
	}
	u, err := url.Parse(upload)
	if err != nil {
		t.Fatalf("parse upload url %q: %v", upload, err)
	}
	if u.Path != "/storages/"+key {
		t.Errorf("upload path = %q, want /storages/%s", u.Path, key)
	}
	expires, err := strconv.ParseInt(u.Query().Get("expires"), 10, 64)
	if err != nil {
		t.Fatalf("upload expires not an integer: %v", err)
	}
	if u.Query().Get("sig") != LocalSign(secret, "PUT", key, expires, "application/pdf") {
		t.Errorf("upload sig does not bind PUT method and content type")
	}

	// the download url is GET-signed and content-type independent
	download, err := client.GetPresignedGetURL(key, time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedGetURL: %v", err)
	}
	d, err := url.Parse(download)
	if err != nil {
		t.Fatalf("parse download url %q: %v", download, err)
	}
	dExpires, err := strconv.ParseInt(d.Query().Get("expires"), 10, 64)
	if err != nil {
		t.Fatalf("download expires not an integer: %v", err)
	}
	if d.Query().Get("sig") != LocalSign(secret, "GET", key, dExpires, "") {
		t.Errorf("download sig does not bind GET method")
	}
	if d.Query().Get("sig") == u.Query().Get("sig") {
		t.Errorf("upload and download signatures must differ")
	}

	// LocalVerify round trips and rejects tampering on every bound field
	cases := []struct {
		name        string
		method      string
		key         string
		expires     int64
		contentType string
		sig         string
		want        bool
	}{
		{"valid upload", "PUT", key, expires, "application/pdf", u.Query().Get("sig"), true},
		{"wrong method", "GET", key, expires, "application/pdf", u.Query().Get("sig"), false},
		{"wrong key", "PUT", "company-a/other.pdf", expires, "application/pdf", u.Query().Get("sig"), false},
		{"wrong expires", "PUT", key, expires + 1, "application/pdf", u.Query().Get("sig"), false},
		{"wrong content type", "PUT", key, expires, "image/png", u.Query().Get("sig"), false},
		{"tampered signature", "PUT", key, expires, "application/pdf", strings.Repeat("0", len(u.Query().Get("sig"))), false},
		{"valid download", "GET", key, dExpires, "", d.Query().Get("sig"), true},
		{"download with content type", "GET", key, dExpires, "application/pdf", d.Query().Get("sig"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocalVerify(secret, tc.method, tc.key, tc.expires, tc.contentType, tc.sig); got != tc.want {
				t.Errorf("LocalVerify = %v, want %v", got, tc.want)
			}
		})
	}

	// content type normalization is symmetric
	ct := "  Application/PDF  "
	normalized := LocalSign(secret, "PUT", key, expires, ct)
	if normalized != LocalSign(secret, "PUT", key, expires, "application/pdf") {
		t.Errorf("content type must be normalized before signing")
	}

	// LocalVerify does not itself enforce expiry (callers do)
	past := time.Now().Add(-time.Hour).Unix()
	pastSig := LocalSign(secret, "PUT", key, past, "application/pdf")
	if !LocalVerify(secret, "PUT", key, past, "application/pdf", pastSig) {
		t.Errorf("LocalVerify must not reject on expiry by itself")
	}

	// without a public base the legacy file-path behavior is kept
	legacy, err := NewLocal(Config{Type: "zahir-local", Name: "s", Endpoint: dir, SecretKey: secret})
	if err != nil {
		t.Fatalf("NewLocal legacy: %v", err)
	}
	if got, err := legacy.GetPresignedUploadURL(key, "application/pdf", time.Minute); err != nil || got != filepath.Join(dir, key) {
		t.Errorf("legacy GetPresignedUploadURL = %q, %v; want file path", got, err)
	}
	if got := legacy.GetPublicURL(key); got != filepath.Join(dir, key) {
		t.Errorf("legacy GetPublicURL = %q, want file path", got)
	}
}

func TestPublicURLs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		key  string
		want string
	}{
		{
			name: "s3 aws with region",
			cfg:  Config{Type: "zahir-s3", Name: "attachments", Endpoint: "s3.amazonaws.com", Region: "ap-southeast-3"},
			key:  "sub/file.txt",
			want: "https://attachments.s3.ap-southeast-3.amazonaws.com/sub/file.txt",
		},
		{
			name: "s3 aws without region",
			cfg:  Config{Type: "zahir-s3", Name: "attachments", Endpoint: "https://s3.amazonaws.com"},
			key:  "file.txt",
			want: "https://attachments.s3.amazonaws.com/file.txt",
		},
		{
			name: "s3 custom endpoint",
			cfg:  Config{Type: "zahir-s3", Name: "att", Endpoint: "s3.custom.example.com"},
			key:  "a/b",
			want: "https://att.s3.custom.example.com/a/b",
		},
		{
			name: "minio http",
			cfg:  Config{Type: "private-minio", Name: "files", Endpoint: "http://minio.local:9000"},
			key:  "dir/f.txt",
			want: "http://minio.local:9000/files/dir/f.txt",
		},
		{
			name: "minio https",
			cfg:  Config{Type: "private-minio", Name: "files", Endpoint: "https://s3.ax.minio.io"},
			key:  "/leading/trimmed",
			want: "https://s3.ax.minio.io/files/leading/trimmed",
		},
		{
			name: "supabase",
			cfg:  Config{Type: "private-supabase", Name: "att", Bucket: "files", Endpoint: "https://storage.abc123.supabase.co"},
			key:  "img/1.png",
			want: "https://abc123.supabase.co/storage/v1/object/public/files/img/1.png",
		},
		{
			name: "oss https",
			cfg:  Config{Type: "private-oss", Name: "att", Endpoint: "https://oss-ap-southeast-1.aliyuncs.com"},
			key:  "doc/x.pdf",
			want: "https://att.oss-ap-southeast-1.aliyuncs.com/doc/x.pdf",
		},
		{
			name: "oss http",
			cfg:  Config{Type: "private-oss", Name: "att", Endpoint: "http://oss-ap-southeast-1.aliyuncs.com"},
			key:  "doc/x.pdf",
			want: "http://att.oss-ap-southeast-1.aliyuncs.com/doc/x.pdf",
		},
		{
			name: "gcs",
			cfg:  Config{Type: "acme-gcs", Name: "att", SecretKey: string(gcsTestKey(t))},
			key:  "img/1.png",
			want: "https://storage.googleapis.com/att/img/1.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.GetPublicURL(tt.key); got != tt.want {
				t.Errorf("GetPublicURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTypePassthrough(t *testing.T) {
	cfg := Config{Type: "acme-supabase", Name: "att", Endpoint: "https://storage.r1.supabase.co"}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Type() != "acme-supabase" {
		t.Errorf("Type() = %q", c.Type())
	}
	if c.Name() != "att" {
		t.Errorf("Name() = %q", c.Name())
	}
	if c.Kind() != KindSupabase {
		t.Errorf("Kind() = %q", c.Kind())
	}
}

func TestSupabaseHost(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://storage.abc123.supabase.co", "abc123.supabase.co"},
		{"storage.abc123.supabase.co", "abc123.supabase.co"},
		{"http://storage.abc123.supabase.co/", "abc123.supabase.co"},
		{"https://supabase.example.com", "supabase.example.com"},
	}
	for _, tt := range tests {
		if got := supabaseHost(tt.in); got != tt.want {
			t.Errorf("supabaseHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOSSPresignShape(t *testing.T) {
	c, err := NewOSS(Config{Name: "att", AccessKey: "AK", SecretKey: "SK", Endpoint: "https://oss-ap-southeast-1.aliyuncs.com"})
	if err != nil {
		t.Fatalf("NewOSS: %v", err)
	}
	u, err := c.GetPresignedUploadURL("data/file.txt", "text/plain", time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedUploadURL: %v", err)
	}
	if !strings.HasPrefix(u, "https://att.oss-ap-southeast-1.aliyuncs.com/data/file.txt?") {
		t.Errorf("unexpected upload URL %q", u)
	}
	for _, param := range []string{"x-oss-credential", "x-oss-expires", "x-oss-signature", "x-oss-signature-version"} {
		if !strings.Contains(u, param+"=") {
			t.Errorf("upload URL missing %q query param: %s", param, u)
		}
	}
}

func TestGCSPresignShape(t *testing.T) {
	c, err := NewGCS(Config{
		Type:      "private-gcs",
		Name:      "att",
		AccessKey: "storage-test@test-project.iam.gserviceaccount.com",
		SecretKey: string(gcsTestKey(t)),
	})
	if err != nil {
		t.Fatalf("NewGCS: %v", err)
	}
	u, err := c.GetPresignedUploadURL("data/file.txt", "text/plain", time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedUploadURL: %v", err)
	}
	if !strings.HasPrefix(u, "https://storage.googleapis.com/att/data/file.txt?") {
		t.Errorf("unexpected upload URL %q", u)
	}
	for _, param := range []string{"X-Goog-Algorithm=GOOG4-RSA-SHA256", "X-Goog-Credential", "X-Goog-Expires", "X-Goog-SignedHeaders=content-type", "X-Goog-Signature"} {
		if !strings.Contains(u, param) {
			t.Errorf("upload URL missing %q: %s", param, u)
		}
	}
	gu, err := c.GetPresignedGetURL("data/file.txt", time.Minute)
	if err != nil {
		t.Fatalf("GetPresignedGetURL: %v", err)
	}
	if !strings.HasPrefix(gu, "https://storage.googleapis.com/att/data/file.txt?") {
		t.Errorf("unexpected get URL %q", gu)
	}
	if !strings.Contains(gu, "X-Goog-Signature=") {
		t.Errorf("get URL missing signature: %s", gu)
	}
}

func TestGCSPresignWithoutKey(t *testing.T) {
	if _, err := NewGCS(Config{Type: "gcs", Name: "att", AccessKey: "usergranteduser@developer.gserviceaccount.com"}); err == nil {
		t.Error("NewGCS should reject an access key without a private key")
	}
}

func TestSupportedKinds(t *testing.T) {
	if got := SupportedKinds(); len(got) != 6 {
		t.Errorf("SupportedKinds len = %d, want 6", len(got))
	}
}
