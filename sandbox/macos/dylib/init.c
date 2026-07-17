#define _GNU_SOURCE
#include "interpose.h"
#include "../../common/message.h"
#include "../../common/sandboxd.h"
#include "../../common/socket.h"
#include <dlfcn.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int daemon_fd = -1;
pid_t registered_pid = 0;

static const char *rawenv(const char *name) {
    static char *(*fn)(const char *);
    if (!fn)
        fn = (char *(*)(const char *))dlsym(RTLD_NEXT, "getenv");
    return fn ? fn(name) : NULL;
}

const char *sandbox_rawenv(const char *name) { return rawenv(name); }

static void connect_daemon(void) {
    const char *p = rawenv("SANDBOX_DAEMON_SOCKET");
    if (!p)
        return;
    daemon_fd = sb_connect(p);
    if (daemon_fd < 0)
        return;
    registered_pid = getpid();
    char b[256];
    snprintf(b, sizeof(b), "%s %d %d %s", SANDBOXD_CMD_REGISTER, registered_pid, getppid(),
             rawenv("_") ? rawenv("_") : "unknown");
    msg_send_line(daemon_fd, b);
    char r[16];
    msg_recv_line(daemon_fd, r, sizeof(r));
}

int ensure_daemon(void) {
    if (daemon_fd < 0)
        connect_daemon();
    return daemon_fd < 0 ? -1 : 0;
}

void child_register(void) {
    if (daemon_fd >= 0) {
        close(daemon_fd);
        daemon_fd = -1;
    }
    connect_daemon();
}

__attribute__((constructor)) static void sandbox_start(void) { connect_daemon(); }