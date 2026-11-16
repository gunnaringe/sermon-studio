# Object storage

The studio is getting the option to keep everything in an S3-compatible bucket, with the server's disk only a cache
([#86](https://github.com/gunnaringe/sermon-studio/issues/86)). Any provider with the S3 API works: Cloudflare R2,
Contabo, Backblaze B2, Hetzner, AWS, or your own (MinIO, Garage, RustFS).

With the `--blob-*` settings, `serve` keeps a copy of `--data-dir` in the bucket as it works. Without them, nothing
changes: everything is only in `--data-dir` (the desktop app has no bucket at all).

**Not there yet:** starting a new machine from the bucket (an empty disk that gets everything back), and the disk as
a cache of limited size. Until then the bucket is a copy kept up to date, and the disk still has everything.

## What goes to the bucket, and when

- **The state files** (`church.json`, `members.json`, `shares.json`, `users.json`, a recording's `recording.json` …) are
  in the bucket before the request that changed them is answered. If the bucket does not take one, the request fails
  and the file is as it was, so the disk and the bucket do not disagree.
- **A recording's draft** (`draft.json`, what the editor keeps as you edit) goes at most every 30 seconds.
- **Every other file** (the original, the files made, transcripts, clips, the logo) goes in the background once it is
  whole. What is waiting is kept in `<data-dir>/.blob/journal.json`, so a restart goes on with it, and a provider that
  is down only delays it.
- **A rename or removal** by the studio is done in the bucket too: a copy, then a delete (S3 has no rename). That
  includes the trash: a recording put in the trash is moved there in the bucket, and emptying the trash deletes it.
- **Only on this machine:** the login sessions (users log in again after a move), the inboxes (a recording counts once
  it is taken in), the cache (waveforms and previews, made again), the models, the transcript queue's parts, and
  half-written files.

At start, then every hour, the studio puts in the bucket whatever it lacks (a file changed outside the studio, one a
crash left out) and reports objects only the bucket has. **It never deletes an object because the disk lacks the
file**: an empty or wrong disk cannot empty the bucket.

## One studio per bucket

A studio holds the bucket's lease (the object `_lease`: which studio, until when), renewed every minute. Another
studio, or a second copy, waits up to 3½ minutes for a lease that is still running, then gives up. A clean stop gives
the lease back, so the new container of an update starts at once; after a crash, the same data dir takes it back at
once (its id is in `<data-dir>/.blob/id`). A studio that finds its lease taken stops. With conditional puts (see
`admin blob check`) taking the lease is safe even when two start at the same moment; without, it is read back.

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
