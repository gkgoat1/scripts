/* Guest-side path resolution helpers. */
#ifndef SANDBOX_COMMON_PATH_H
#define SANDBOX_COMMON_PATH_H

#include <stddef.h>

/* Normalize an absolute path in-place, collapsing ".", "..", and repeated
 * separators. The result is written into out and is always NUL-terminated.
 */
void sb_normalize_path(char *out, size_t out_size, const char *path);

/* Resolve a possibly relative path against the current working directory,
 * normalize it, and return a pointer to out. If resolution fails, the
 * original path is returned.
 */
const char *sb_resolve_path(const char *path, char *out, size_t out_size);

#endif /* SANDBOX_COMMON_PATH_H */