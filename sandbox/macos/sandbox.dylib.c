#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <sys/types.h>
#include <mach-o/dyld.h>

extern char **environ;

typedef int (*execve_fn)(const char *, char *const[], char *const[]);
static execve_fn real_execve;
static int resolving;
static int daemon_fd = -1;
static pid_t registered_pid;

static const char *rawenv(const char *name) {
    static char *(*fn)(const char *);
    if (!fn)
        fn = (char *(*)(const char *))dlsym(RTLD_NEXT, "getenv");
    return fn ? fn(name) : NULL;
}

static void connect_daemon(void) {
    const char *p = rawenv("SANDBOX_DAEMON_SOCKET");
    if (!p)
        return;
    daemon_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (daemon_fd < 0)
        return;
    fcntl(daemon_fd, F_SETFD, FD_CLOEXEC);
    struct sockaddr_un a = {.sun_family = AF_UNIX};
    snprintf(a.sun_path, sizeof a.sun_path, "%s", p);
    if (connect(daemon_fd, (void *)&a, sizeof a) < 0) {
        close(daemon_fd);
        daemon_fd = -1;
        return;
    }
    registered_pid = getpid();
    char b[256];
    snprintf(b, sizeof b, "REGISTER %d %d %s\n", registered_pid, getppid(),
             rawenv("_") ? rawenv("_") : "unknown");
    write(daemon_fd, b, strlen(b));
    char r[16];
    read(daemon_fd, r, sizeof r);
}

static void init_real(void) {
    if (!real_execve && !resolving) {
        resolving = 1;
        real_execve = (execve_fn)dlsym(RTLD_NEXT, "execve");
        resolving = 0;
    }
    if (daemon_fd < 0)
        connect_daemon();
}

static ssize_t request_response(const char *request, char *resp, size_t resp_size) {
    if (daemon_fd < 0)
        connect_daemon();
    if (daemon_fd < 0)
        return -1;
    char b[4096];
    snprintf(b, sizeof b, "%s\n", request);
    if (write(daemon_fd, b, strlen(b)) < 0) {
        close(daemon_fd);
        daemon_fd = -1;
        return -1;
    }
    size_t i = 0;
    char ch;
    while (i + 1 < resp_size) {
        ssize_t n = read(daemon_fd, &ch, 1);
        if (n <= 0)
            return -1;
        resp[i++] = ch;
        if (ch == '\n')
            break;
    }
    if (i == 0)
        return -1;
    resp[i] = 0;
    return (ssize_t)i;
}

static bool ask(const char *request, const char *yes) {
    char b[64];
    if (request_response(request, b, sizeof b) < 0)
        return false;
    return strncmp(b, yes, strlen(yes)) == 0;
}

static void child_register(void) {
    if (daemon_fd >= 0) {
        close(daemon_fd);
        daemon_fd = -1;
    }
    connect_daemon();
}

__attribute__((constructor)) static void sandbox_start(void) { connect_daemon(); }

int fork(void) {
    static int (*real_fork)(void);
    if (!real_fork)
        real_fork = (int (*)(void))dlsym(RTLD_NEXT, "fork");
    int r = real_fork();
    if (r == 0)
        child_register();
    return r;
}

static void normalize_path(char *out, size_t out_size, const char *path) {
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
        size_t len = end - p;
        if (len == 0 || (len == 1 && p[0] == '.')) {
            /* skip */
        } else if (len == 2 && p[0] == '.' && p[1] == '.') {
            if (nseg > 0)
                nseg--;
        } else {
            char *s = malloc(len + 1);
            if (s) {
                memcpy(s, p, len);
                s[len] = 0;
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
    if (pos == 1)
        out[pos] = 0;
    else
        out[pos] = 0;
    for (size_t i = 0; i < nseg; i++)
        free(seg[i]);
    free(seg);
}

static const char *resolve_path(const char *path, char *out, size_t out_size) {
    if (path[0] == '/')
        return path;
    char *cwd = getcwd(NULL, 0);
    if (!cwd)
        return path;
    char tmp[4096];
    snprintf(tmp, sizeof tmp, "%s/%s", cwd, path);
    free(cwd);
    normalize_path(out, out_size, tmp);
    return out;
}

static bool open_allowed(const char *path, int flags) {
    char resolved[4096];
    const char *p = resolve_path(path, resolved, sizeof resolved);
    char req[4096];
    snprintf(req, sizeof req, "OPEN %d %s %d", getpid(), p, flags);
    char resp[64];
    if (request_response(req, resp, sizeof resp) < 0)
        return false;
    if (strncmp(resp, "ALLOWED", 7) == 0 || strncmp(resp, "UPDATED", 7) == 0)
        return true;
    if (strncmp(resp, "RO", 2) == 0) {
        if (flags & (O_WRONLY | O_RDWR | O_APPEND | O_CREAT | O_TRUNC))
            return false;
        return true;
    }
    return false;
}

int execve(const char *path, char *const argv[], char *const envp[]) {
    init_real();
    if (!real_execve || resolving) {
        errno = ENOSYS;
        return -1;
    }
    char resolved[4096];
    const char *p = resolve_path(path, resolved, sizeof resolved);
    if (!open_allowed(p, 0)) {
        errno = EACCES;
        return -1;
    }
    int r = real_execve(path, argv, envp);
    return r;
}

int execv(const char *path, char *const argv[]) { return execve(path, argv, environ); }

char *getenv(const char *name) {
    static char *(*real_getenv)(const char *);
    if (!real_getenv)
        real_getenv = (char *(*)(const char *))dlsym(RTLD_NEXT, "getenv");
    if (!real_getenv)
        return NULL;
    const char *policy = real_getenv("SANDBOX_ENV_POLICY");
    if (policy && strcmp(name, "SANDBOX_ENV_POLICY") &&
        strcmp(name, "SANDBOX_DAEMON_SOCKET")) {
        char exe[1024];
        uint32_t size = sizeof(exe);
        if (_NSGetExecutablePath(exe, &size) == 0) {
            char req[2048];
            snprintf(req, sizeof req, "ENV %d %s %s", getpid(), exe, name);
            if (!ask(req, "ALLOW"))
                return "";
        }
    }
    return real_getenv(name);
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