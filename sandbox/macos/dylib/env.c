#define _GNU_SOURCE
#include <dlfcn.h>
#include <mach-o/dyld.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include "interpose.h"
#include "../../common/message.h"
#include "../../common/sandboxd.h"

static char *(*real_getenv)(const char *);

char *getenv(const char *name) {
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
            if (ensure_daemon() == 0) {
                char req[2048];
                snprintf(req, sizeof(req), "%s %d %s %s", SANDBOXD_CMD_ENV, getpid(), exe, name);
                if (msg_send_line(daemon_fd, req) == 0) {
                    char resp[64];
                    if (msg_recv_line(daemon_fd, resp, sizeof(resp)) > 0 &&
                        strncmp(resp, "ALLOW", 5) != 0) {
                        return "";
                    }
                }
            }
        }
    }
    return real_getenv(name);
}