# Object storage

The studio is getting the option to keep everything in an S3-compatible bucket, with the server's disk only a cache
([#86](https://github.com/gunnaringe/sermon-studio/issues/86)). Any provider with the S3 API works: Cloudflare R2,
Contabo, Backblaze B2, Hetzner, AWS, or your own (MinIO, Garage, RustFS).

**So far** there are the tools to set up and check a bucket, and each recording's `recording.json` lists its files
(`files`: size, time and SHA-256), which the bucket will be checked against. The studio itself does not use a bucket
yet: its files stay in `--data-dir`.

## Settings

| Flag | |
|---|---|
| `--blob-endpoint` | The provider's S3 address, with `https://` |
| `--blob-region` | The bucket's region (R2: `auto`; with none, leave it out) |
| `--blob-bucket` | The bucket |
| `--blob-prefix` | Optional: keep everything under this folder in the bucket |
| `--blob-path-style` | Address the bucket as `<endpoint>/<bucket>` (Contabo, MinIO, Garage, RustFS need it) |
| `--blob-access-key` | The access key's id |
| `--blob-secret-key-file` | A file with the secret key (never a flag or an environment variable) |

Like every flag, they can also be `SERMON_STUDIO_BLOB_…` environment variables, except the secret key, which is only
read from its file.

| Provider | Endpoint | Region | Path style |
|---|---|---|---|
| Cloudflare R2 | `https://<account id>.r2.cloudflarestorage.com` | `auto` | either |
| Contabo | `https://<region>.contabostorage.com`, e.g. `eu2` (as its panel shows) | as its panel shows | yes |
| Backblaze B2 | `https://s3.<region>.backblazeb2.com` | e.g. `eu-central-003` | either |
| Hetzner | `https://<location>.your-objectstorage.com` | e.g. `fsn1` | either |

Give the key access to that bucket only.

## Setting up a bucket

```sh
# Let the studio's pages play and download from the bucket (its CORS rule):
sermon-studio admin blob cors <flags> --origin https://my.sermon-studio.app --origin https://kirke.sermon-studio.app

# Try what the studio needs, and print what works:
sermon-studio admin blob check <flags> --origin https://my.sermon-studio.app
```

`check` stores a few objects under `_check-…` and removes them after. It tries:

- **Needed:** put, head, get, list, a multipart upload (6 MB in two parts), copy, delete, and a presigned GET with Range.
- **With a way round:**
  - **The file name of a download** (`response-content-disposition`): without it, downloads go through the studio.
  - **CORS:** without it, downloads go through the studio.
  - **Conditional puts** (`If-None-Match`, `If-Match`): without them, a second studio on the same bucket is stopped by a
    plain lease object instead.

It ends with an error when something needed is missing.

## Tests

`scripts/test.sh blob` runs the tests of package `blob` against RustFS in a container (MinIO no longer publishes
images). CI does the same on Linux (`test.yml`).
