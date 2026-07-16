#define _GNU_SOURCE
#include "interpose.h"
#include "../../common/path.h"
#include <dlfcn.h>
#include <errno.h>
#include <unistd.h>

extern char **environ;

typedef int (*execve_fn)(const char *, char *const[], char *const[]);

static execve_fn real_execve;
static int resolving;

static void init_real(void) {
    if (!real_execve && !resolving) {
        resolving = 1;
        real_execve = (execve_fn)dlsym(RTLD_NEXT, "execve");
        resolving = 0;
    }
    if (daemon_fd < 0)
        ensure_daemon();
}

int execve(const char *path, char *const argv[], char *const envp[]) {
    init_real();
    if (!real_execve || resolving) {
        errno = ENOSYS;
        return -1;
    }
    char resolved[4096];
    const char *p = sb_resolve_path(path, resolved, sizeof(resolved));
    if (!open_allowed(p, 0)) {
        errno = EACCES;
        return -1;
    }
    return real_execve(path, argv, envp);
}

int execv(const char *path, char *const argv[]) { return execve(path, argv, environ); }

int fork(void) {
    static int (*real_fork)(void);
    if (!real_fork)
        real_fork = (int (*)(void))dlsym(RTLD_NEXT, "fork");
    int r = real_fork();
    if (r == 0)
        child_register();
    return r;
}