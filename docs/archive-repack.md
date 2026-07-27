# archive-repack

`archive-repack` recursively walks a source directory and writes new archives
without extracting archive contents to disk.

```sh
go run ./archive-repack ./incoming ./repacked.tar.gz
go build -o bin/archive-repack ./archive-repack
./bin/archive-repack ./incoming ./repacked.zip
```

The destination extension selects its format: `.zip`, `.tar`, `.tar.gz`/
`.tgz`, `.tar.bz2`/`.tbz`, and `.tar.xz`/`.txz` are supported. The destination
must not exist.

Regular files keep their relative locations under the source folder's basename.
Each `.zip`, `.tar`, compressed tar, or single-file `.gz` found anywhere below
that folder is read in place and omitted from the result. Zip/tar contents are
placed in a sibling directory named after the archive (for example, `logs.zip`
becomes `logs/...`); `report.gz` becomes `report`. This applies only to archive
files found while walking the source directory: archives embedded inside another
archive are copied as members rather than recursively expanded.

Contents are copied in 1 MiB chunks. A `.gz` input is counted in a first
streaming pass only when the output is a tar variant, because a tar header needs
the uncompressed size in advance. It rejects path traversal members, filename
collisions, and source-file symlinks; symlinked directories are not traversed.

## Large destination files

If a write to the destination fails with `EFBIG` / `file too large`, the tool
rebuilds the current archive from completed members (discarding the truncated
member), then retries that member in a continuation archive. Continuation names
preserve the destination extension and append a number before it:

```text
repacked.tar.gz
repacked-2.tar.gz
repacked-3.tar.gz
```

Each completed archive is valid on its own. A member that cannot fit in an empty
archive still reports an error rather than retrying indefinitely.