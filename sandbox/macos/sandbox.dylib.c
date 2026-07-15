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
}

static char *daemon_rewrite(const char *path) {
    const char *socket_path = getenv("SANDBOX_DAEMON_SOCKET");
    if (!socket_path || !*socket_path) return NULL;
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return NULL;
    struct sockaddr_un address = {.sun_family = AF_UNIX};
    snprintf(address.sun_path, sizeof(address.sun_path), "%s", socket_path);
    if (connect(fd, (struct sockaddr *)&address, sizeof(address)) < 0) { close(fd); return NULL; }
    dprintf(fd, "REWRITE %s\n", path);
    char response[4096]; ssize_t n = read(fd, response, sizeof(response) - 1); close(fd);
    if (n <= 3 || strncmp(response, "OK ", 3) != 0) return NULL;
    response[n] = '\0'; char *newline = strchr(response + 3, '\n'); if (newline) *newline = '\0';
    return strdup(response + 3);
}

int execve(const char *path, char *const argv[], char *const envp[]) {
    init_real();
    if (!real_execve || resolving) { errno = ENOSYS; return -1; }
    char *rewritten = daemon_rewrite(path);
    int result = real_execve(rewritten ? rewritten : path, argv, envp);
    int saved = errno; free(rewritten); errno = saved; return result;
}

/* These declarations ensure callers that resolve the common exec symbols still
   enter the execve interception. The libc implementations normally call execve. */
int execv(const char *path, char *const argv[]) { return execve(path, argv, environ); }
static bool allowed_path(const char *path) {
    const char *home = getenv("HOME");
    if (home && ((!strncmp(path, "/Documents", 10)) || strstr(path, "/Desktop/") || strstr(path, "/Downloads/"))) return false;
    const char *base = strrchr(path, '/'); base = base ? base + 1 : path;
    if (base[0] == '.' && strcmp(base, ".") && strcmp(base, "..")) {
        return !strcmp(base, ".zshrc") || !strcmp(base, ".bashrc") || !strcmp(base, ".profile") || !strcmp(base, ".bash_profile");
    }
    return true;
}

typedef int (*open_fn)(const char *, int, ...);
int open(const char *path, int flags, ...) {
    static open_fn real_open; if (!real_open) real_open = (open_fn)dlsym(RTLD_NEXT, "open");
    if (!allowed_path(path)) { errno = EACCES; return -1; }
    va_list ap; va_start(ap, flags); mode_t mode = (mode_t)va_arg(ap, int); va_end(ap);
    if (flags & O_WRONLY || flags & O_RDWR || flags & O_CREAT || flags & O_TRUNC) {
        const char *base = strrchr(path, '/'); base = base ? base + 1 : path;
        if (base[0] == '.') { errno = EACCES; return -1; }
    }
    return real_open(path, flags, mode);
}