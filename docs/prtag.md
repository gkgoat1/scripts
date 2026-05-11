# `.prtag` file format (v1)

`.prtag` files are small project markers: a required **name**, free-form **body**, and a reserved **metadata section** (empty for now).

## Canonical layout

```text
name:
text text text
---
[metadata]
```

## Sections

### Name header (required)
- The first line must be `name:` where `name` is a non-empty string.
- `name` is **not bracketed**.

### Body (optional)
- The body is everything after the name header line up to the delimiter line `---` (or EOF).
- Body is treated as opaque text (no structure).

### Delimiter
- The delimiter line is exactly:

```text
---
```

### Metadata section (reserved)
- If the delimiter is present, the next non-empty line must be a bracketed section header like `[metadata]`.
- For v1, the metadata section must be empty (only whitespace/newlines after the bracketed header are allowed).
- Writers should still emit the delimiter + a bracketed header even when metadata is empty.

## Writer normalization
- Writers should emit:
  - `name:\n`
  - body text (verbatim), ensuring there is a trailing newline before the delimiter
  - `---\n`
  - a bracketed header line `[...]` (defaulting to `[metadata]` if unspecified)
  - no metadata content

