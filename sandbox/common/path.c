#define _GNU_SOURCE
#include "path.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

void sb_normalize_path(char *out, size_t out_size, const char *path) {
    if (out_size == 0)
        return;

    char **seg = malloc(out_size * sizeof(char *));
    if (!seg) {
        snprintf(out, out_size, "%s", path);
        return;
    }

    size_t nseg = 0;
    const char *p = path;
    while (*p) {
        const char *end = p;
        while (*end && *end != '/')
            end++;
        size_t len = (size_t)(end - p);
        if (len == 0 || (len == 1 && p[0] == '.')) {
            /* skip */
        } else if (len == 2 && p[0] == '.' && p[1] == '.') {
            if (nseg > 0)
                nseg--;
        } else {
            char *s = malloc(len + 1);
            if (s) {
                memcpy(s, p, len);
                s[len] = '\0';
                seg[nseg++] = s;
            }
        }
        p = end;
        while (*p == '/')
            p++;
    }

    size_t pos = 0;
    out[pos++] = '/';
    for (size_t i = 0; i < nseg; i++) {
        size_t l = strlen(seg[i]);
        if (pos + l + 2 > out_size)
            break;
        if (i > 0)
            out[pos++] = '/';
        memcpy(out + pos, seg[i], l);
        pos += l;
    }
    out[pos] = '\0';

    for (size_t i = 0; i < nseg; i++)
        free(seg[i]);
    free(seg);
}

const char *sb_resolve_path(const char *path, char *out, size_t out_size) {
    if (path[0] == '/')
        return path;
    char *cwd = getcwd(NULL, 0);
    if (!cwd)
        return path;
    char tmp[4096];
    snprintf(tmp, sizeof(tmp), "%s/%s", cwd, path);
    free(cwd);
    sb_normalize_path(out, out_size, tmp);
    return out;
}