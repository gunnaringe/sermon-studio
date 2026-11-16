# Object storage

The studio can keep everything in an S3-compatible bucket, with the server's disk only a cache
([#86](https://github.com/gunnaringe/sermon-studio/issues/86)). Any provider with the S3 API works: Cloudflare R2,
Contabo, Backblaze B2, Hetzner, AWS, or your own (MinIO, Garage, RustFS).

With the `--blob-*` settings, `serve` keeps a copy of `--data-dir` in the bucket as it works. Without them, nothing
changes: everything is only in `--data-dir` (the desktop app has no bucket at all).

The disk keeps the JSON files and what is being worked on; a recording's file not used for 30 days is then only in
the bucket, fetched again when needed ([Files only in the bucket](#files-only-in-the-bucket)). A new machine starts
from the bucket with only the small files ([A new machine](#a-new-machine)).

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

## Files only in the bucket

A recording's file can be kept in the bucket alone, and so can a church's intro and outro clips and its logo. The files
record says so (`"bucket": true` under `files` in the recording's `recording.json`, or the church's `church.json`), and
the studio still lists it, shows it and plans runs with it. It is fetched when something needs to read it: a run, an
analysis, a sample, a transcript, a download, a rename, listening to a clip in Settings, the logo. Each fetched file is checked against its
SHA-256 in the record, and keeps the time it had, so it is not taken for changed or put in the bucket again. Removing it
(deleting the original or a format, the trash, a clip, the logo) removes it from the bucket and the record.

Listening to or downloading such a file (in the studio, or from a download share) does not fetch it: the browser is sent
to an address of the bucket for that one file, which works for 6 hours and names the file. The share page's player
may follow it (its `media-src` has the bucket's origin). A share's ticket still decides what may be had, by format; while
the file comes from the bucket, the studio does not see the download going on. A transcript is fetched and served as
before, as the page reads it with `fetch`.

A file leaves the disk only once the record has it as it is now and the bucket has it as uploaded after its last
change, with nothing for it waiting in the journal and no work going on in its recording (for a clip or the logo: in
the church, nor a clip being taken in). Every JSON file (a transcript's too), drafts and the cache always stay on the
disk.

**When a file leaves the disk:** when it has not been used for `--blob-keep-days` (30 by default; 0 keeps them), and,
with `--blob-cache-size`, while the recorded files on the disk (not the JSON) take more than that, the ones used
longest ago first. Used is when it was last made, fetched, or read by the studio: a run, an analysis, a sample, a transcript, a
download from the disk. A rename is not a use, and listening to or downloading a file only in the bucket does not
fetch it. The times are kept in `<data-dir>/.blob/used.json`; a file not there counts from its own time. The studio
looks at start (after its first sweep) and every hour, and logs what it took off. A church's storage and quota count
its files wherever they are.

## A new machine

Started on an empty data dir (no file in it that goes to the bucket: empty folders, the cache and the models do not
count) with a bucket that has files, the studio first takes the lease, then fetches what must be on the disk before it
serves anything: the JSON files and any file no record has. The files in a record (a recording's, or a church's clips
and logo) stay in the bucket, marked so, and are fetched when needed; one the record has and the bucket lacks is
dropped from the record, and logged. A restore cut short (`.blob/restoring` is still there) is done again at the next
start. A data dir with files in it starts as it is: the two are never merged.

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
| `--blob-keep-days` | Days a recording's file not used stays on the disk (30; 0 = always) |
| `--blob-cache-size` | Optional: the most the recordings' files may take on the disk, e.g. `20GB` |

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

`SERMON_TEST_EVICT=1 go test ./web/` runs the studio's tests with every recording's file kept only in the bucket as
soon as it is recorded and uploaded: what reads a file without fetching it shows up there. Tests that read the disk
themselves skip in this mode (`skipIfEvicting`).
