#define _GNU_SOURCE
#include "interpose.h"
#include "../../common/message.h"
#include "../../common/path.h"
#include "../../common/sandboxd.h"
#include <dlfcn.h>
#include <errno.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

bool open_allowed(const char *path, int flags) {
    char resolved[4096];
    const char *p = sb_resolve_path(path, resolved, sizeof(resolved));
    char req[4096];
    snprintf(req, sizeof(req), "%s %d %s %d", SANDBOXD_CMD_OPEN, getpid(), p, flags);
    char resp[64];
    if (ensure_daemon() < 0)
        return false;
    if (msg_send_line(daemon_fd, req) < 0 ||
        msg_recv_line(daemon_fd, resp, sizeof(resp)) < 0)
        return false;
    if (strncmp(resp, SANDBOXD_RESP_ALLOWED, strlen(SANDBOXD_RESP_ALLOWED)) == 0 ||
        strncmp(resp, SANDBOXD_RESP_UPDATED, strlen(SANDBOXD_RESP_UPDATED)) == 0)
        return true;
    if (strncmp(resp, SANDBOXD_RESP_RO, strlen(SANDBOXD_RESP_RO)) == 0) {
        if (flags & (O_WRONLY | O_RDWR | O_APPEND | O_CREAT | O_TRUNC))
            return false;
        return true;
    }
    return false;
}

typedef int (*open_fn)(const char *, int, ...);
int open(const char *p, int f, ...) {
    static open_fn real;
    if (!real)
        real = (open_fn)dlsym(RTLD_NEXT, "open");
    if (!open_allowed(p, f)) {
        errno = EACCES;
        return -1;
    }
    va_list a;
    va_start(a, f);
    mode_t m = (mode_t)va_arg(a, int);
    va_end(a);
    return real(p, f, m);
}