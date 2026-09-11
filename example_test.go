package gostorage

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExampleNew demonstrates the full lifecycle of a storage client: a presigned
// upload URL, a presigned download URL and the public URL for one object key.
// It uses the local MinIO from docker-compose.yml (see the README). Presigning
// is computed locally, so the example runs without a running server; the
// bucket only matters for real uploads/downloads.
func ExampleNew() {
	client, err := New(Config{
		Kind:      KindMinio, // docker-compose MinIO (see README)
		Name:      "chum-bucket",
		Endpoint:  "http://localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	})
	if err != nil {
		panic(err)
	}

	key := "krabby-patty/reports/2026-09-11.pdf"

	// Presigned URLs are signed per request; only the public URL is stable.
	uploadURL, err := client.GetPresignedUploadURL(key, "application/pdf", DefaultPresignUploadExpiry)
	if err != nil {
		panic(err)
	}
	fmt.Println(uploadURL != "")

	downloadURL, err := client.GetPresignedGetURL(key, DefaultPresignGetExpiry)
	if err != nil {
		panic(err)
	}
	fmt.Println(downloadURL != "")

	fmt.Println("public URL:", client.GetPublicURL(key))
	// Output:
	// true
	// true
	// public URL: http://localhost:9000/chum-bucket/krabby-patty/reports/2026-09-11.pdf
}

// memoryClient is a tiny in-memory implementation of the Client interface used
// by the examples below. It demonstrates the documented behavior of every
// function on the interface without touching a real storage provider.
type memoryClient struct {
	objects map[string]int64 // key -> size in bytes
	typ     string
	name    string
	kind    Kind
}

func (c memoryClient) GetPresignedUploadURL(key, _ string, _ time.Duration) (string, error) {
	return "https://example.test/signed-put/" + key, nil
}

func (c memoryClient) GetPresignedGetURL(key string, _ time.Duration) (string, error) {
	return "https://example.test/signed-get/" + key, nil
}

func (c memoryClient) GetPublicURL(key string) string {
	return "https://example.test/public/" + key
}

func (c memoryClient) GetSize(prefix string) (int64, error) {
	var total int64
	for key, size := range c.objects {
		if strings.HasPrefix(key, prefix) {
			total += size
		}
	}
	return total, nil
}

func (c memoryClient) ListObjects(prefix string) ([]ObjectInfo, error) {
	var res []ObjectInfo
	for key, size := range c.objects {
		if strings.HasPrefix(key, prefix) {
			res = append(res, ObjectInfo{Key: key, Size: size})
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Key < res[j].Key })
	return res, nil
}

func (c memoryClient) DeleteObject(key string) error {
	delete(c.objects, key)
	return nil
}

func (c memoryClient) Type() string { return c.typ }

func (c memoryClient) Name() string { return c.name }

func (c memoryClient) Kind() Kind { return c.kind }

func newMemoryClient() Client {
	return &memoryClient{
		objects: map[string]int64{
			"krabby-patty/reports/2026-09-11.pdf": 2048,
			"krabby-patty/reports/2026-09-12.pdf": 4096,
		},
		typ:  "acme-s3",
		name: "chum-bucket",
		kind: KindS3,
	}
}

// ExampleClient_GetPresignedUploadURL shows how any Client lets an unqualified
// client (e.g. a browser) upload an object via a signed HTTP PUT URL.
func ExampleClient_GetPresignedUploadURL() {
	client := newMemoryClient()
	url, err := client.GetPresignedUploadURL("krabby-patty/reports/2026-09-11.pdf", "application/pdf", DefaultPresignUploadExpiry)
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	// Output:
	// https://example.test/signed-put/krabby-patty/reports/2026-09-11.pdf
}

// ExampleClient_GetPresignedGetURL shows how any Client lets an unqualified
// client download an object via a signed URL.
func ExampleClient_GetPresignedGetURL() {
	client := newMemoryClient()
	url, err := client.GetPresignedGetURL("krabby-patty/reports/2026-09-11.pdf", DefaultPresignGetExpiry)
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	// Output:
	// https://example.test/signed-get/krabby-patty/reports/2026-09-11.pdf
}

// ExampleClient_GetPublicURL shows the public (unsigned) object URL. The
// object must be readable through the provider's bucket policy.
func ExampleClient_GetPublicURL() {
	client := newMemoryClient()
	fmt.Println(client.GetPublicURL("krabby-patty/reports/2026-09-11.pdf"))
	// Output:
	// https://example.test/public/krabby-patty/reports/2026-09-11.pdf
}

// ExampleClient_GetSize shows how the total bytes accumulated under a prefix
// (e.g. everything owned by one company) is computed.
func ExampleClient_GetSize() {
	client := newMemoryClient()
	total, err := client.GetSize("krabby-patty/")
	if err != nil {
		panic(err)
	}
	fmt.Printf("used: %d bytes\n", total)
	// Output:
	// used: 6144 bytes
}

// ExampleClient_ListObjects shows how every object under a prefix is listed,
// with keys relative to the bucket.
func ExampleClient_ListObjects() {
	client := newMemoryClient()
	objects, err := client.ListObjects("krabby-patty/")
	if err != nil {
		panic(err)
	}
	for _, obj := range objects {
		fmt.Printf("%s (%d bytes)\n", obj.Key, obj.Size)
	}
	// Output:
	// krabby-patty/reports/2026-09-11.pdf (2048 bytes)
	// krabby-patty/reports/2026-09-12.pdf (4096 bytes)
}

// ExampleClient_DeleteObject shows how an object is permanently removed.
func ExampleClient_DeleteObject() {
	client := newMemoryClient()
	if err := client.DeleteObject("krabby-patty/reports/2026-09-11.pdf"); err != nil {
		panic(err)
	}
	total, err := client.GetSize("krabby-patty/")
	if err != nil {
		panic(err)
	}
	fmt.Printf("remaining: %d bytes\n", total)
	// Output:
	// remaining: 4096 bytes
}

// ExampleClient_Type shows the full storage type as configured.
func ExampleClient_Type() {
	client := newMemoryClient()
	fmt.Println(client.Type())
	// Output:
	// acme-s3
}

// ExampleClient_Name shows the logical storage name as configured.
func ExampleClient_Name() {
	client := newMemoryClient()
	fmt.Println(client.Name())
	// Output:
	// chum-bucket
}

// ExampleClient_Kind shows the resolved provider kind.
func ExampleClient_Kind() {
	client := newMemoryClient()
	fmt.Println(client.Kind())
	// Output:
	// s3
}
