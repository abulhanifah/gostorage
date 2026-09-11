# gostorage

Secure & scalable file management library for Go. Effortlessly handle frontend
direct-to-storage uploads, presigned access links, and storage operations.

`gostorage` is a provider-agnostic object storage client for Go. It exposes one
interface across four providers and only ever hands out **presigned** or
**public** URLs — credentials never reach the client:

| Provider          | Kind (`gostorage.Kind`) | Library                                                                                          |
| ----------------- | ------------------------- | ------------------------------------------------------------------------------------------------ |
| AWS S3            | `KindS3`                | [aws-sdk-go-v2/service/s3](https://github.com/aws/aws-sdk-go-v2) (official)                       |
| MinIO             | `KindMinio`             | [minio-go](https://github.com/minio/minio-go) (MinIO's official SDK)                              |
| Supabase Storage  | `KindSupabase`          | [supabase-community/storage-go](https://github.com/supabase-community/storage-go) (official)      |
| Alibaba Cloud OSS | `KindOSS`               | [alibabacloud-oss-go-sdk-v2/oss](https://github.com/aliyun/alibabacloud-oss-go-sdk-v2) (official) |

## Features

- **Presigned upload URLs** — let a browser upload directly to storage with a
  short-lived URL (HTTP PUT).
- **Presigned download URLs** — short-lived read access without credentials.
- **Public URLs** — direct, unsigned object URLs.
- **List & size** — enumerate objects and sum sizes under a prefix
  (e.g. a logical sub-bucket).
- **Delete** — hard-delete objects.

## Install

```sh
go get gostorage
# or point it at this module directly:
go mod edit -replace gostorage=../gostorage
```

## Run MinIO locally (Docker)

Spin up MinIO (official image) with the bundled `docker-compose.yml` for
development, demos and tests:

```sh
docker compose up -d    # start MinIO and create the example bucket
docker compose down     # stop (data stays in the minio_data volume)
docker compose down -v  # stop and wipe all data
```

Two services are started:

- `minio` — the S3-compatible server:
  - S3 API: `http://localhost:9000`
  - Web console: `http://localhost:9001` (login `minioadmin` / `minioadmin`)
- `createbuckets` — one-shot job that waits for MinIO to become healthy and
  creates the `chum-bucket` bucket used by the examples.

Point `gostorage` at it with:

```go
client, err := gostorage.New(gostorage.Config{
	Kind:      gostorage.KindMinio, // path-style URLs
	Name:      "chum-bucket",
	Endpoint:  "http://localhost:9000",
	AccessKey: "minioadmin",
	SecretKey: "minioadmin",
})
```

> Presigning is computed locally and never touches the server, so the examples
> in `example_test.go` run even without Docker. The running server is only
> needed to actually upload, download or list objects.

## Usage

```go
package main

import (
	"fmt"
	"time"

	"gostorage"
)

func main() {
	client, err := gostorage.New(gostorage.Config{
		Kind:      gostorage.KindS3, // s3 | minio | supabase | oss
		Name:      "chum-bucket",
		Endpoint:  "s3.amazonaws.com",
		Region:    "ap-southeast-3",
		AccessKey: "<key>",
		SecretKey: "<secret>",
	})
	if err != nil {
		panic(err)
	}

	key := "krabby-patty/reports/2026-09-11.pdf"

	uploadURL, err := client.GetPresignedUploadURL(key, "application/pdf", 15*time.Minute)
	if err != nil {
		panic(err)
	}
	fmt.Println("PUT this file to:", uploadURL)

	downloadURL, err := client.GetPresignedGetURL(key, time.Hour)
	if err != nil {
		panic(err)
	}
	fmt.Println("GET from:", downloadURL)

	fmt.Println("Public:", client.GetPublicURL(key))

	total, err := client.GetSize("krabby-patty/")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Used storage under krabby-patty: %d bytes\n", total)
}
```

Each provider has a dedicated constructor, so you can skip `Type`:

```go
s3, _      := gostorage.NewS3(cfg)
mini, _    := gostorage.NewMinio(cfg)   // path-style URLs
supa, _    := gostorage.NewSupabase(cfg) // REST API, Bearer <AccessKey>
oss, _     := gostorage.NewOSS(cfg)
```

## Configuration

`gostorage.Config`:

| Field                         | Description                                                                                                                                                             |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Type`                      | Optional label of the form`"<qualifier>-<kind>"` (e.g. `"acme-s3"`) or a bare `"s3"`. Used to derive `Kind` when empty and preserved as returned by `Type()`. |
| `Kind`                      | Raw provider kind (`KindS3`, `KindMinio`, `KindSupabase`, `KindOSS`). Derived from `Type` when empty.                                                         |
| `Name`                      | Logical storage name.                                                                                                                                                   |
| `Bucket`                    | Bucket inside the provider. Defaults to`Name` when empty.                                                                                                             |
| `Endpoint`                  | Provider endpoint, with or without`http(s)://` scheme (TLS by default).                                                                                               |
| `Region`                    | Provider region. Supabase: project reference id (`<ref>.supabase.co`).                                                                                                |
| `AccessKey` / `SecretKey` | Credentials. Supabase`AccessKey` is the anon/service_role API key.                                                                                                    |
| `Context`                   | Base context for operations (defaults to`context.Background()`).                                                                                                      |
| `Timeout`                   | HTTP timeout for OSS raw calls (defaults to 30s).                                                                                                                       |

**Supabase host mapping**: an endpoint `https://storage.<ref>.supabase.co` is
normalized to `<ref>.supabase.co` so REST and public URLs target
`https://<ref>.supabase.co/storage/v1/...`.

## URL styles

- **S3** — virtual-hosted: `https://{bucket}.s3.{region}.amazonaws.com/{key}`
- **MinIO** — path-style: `https://{endpoint}/{bucket}/{key}`
- **Supabase** — `https://{ref}.supabase.co/storage/v1/object/public/{bucket}/{key}`
- **OSS** — virtual-hosted: `https://{bucket}.{endpoint}/{key}`

## License

GPL-3.0 — see [LICENSE](LICENSE).
