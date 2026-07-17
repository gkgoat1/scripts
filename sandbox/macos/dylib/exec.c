#define _GNU_SOURCE
#include "interpose.h"
#include "exec_protocol.h"
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
    if (!real_execve || resolving) { errno = ENOSYS; return -1; }
    char resolved[4096];
    const char *p = sb_resolve_path(path, resolved, sizeof(resolved));
    if (!open_allowed(p, 0)) { errno = EACCES; return -1; }
    struct sbxp_exec_result result = {0};
    if (sbxp_exec_authorize(p, argv, envp, &result) < 0 || !result.allowed || !result.path || !result.argv || !result.env) {
        sbxp_free_exec_result(&result);
        errno = EACCES;
        return -1;
    }
    int rc = real_execve(result.path, result.argv, result.env);
    sbxp_free_exec_result(&result);
    return rc;
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