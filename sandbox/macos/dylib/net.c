#define _GNU_SOURCE
#include "interpose.h"
#include "../../common/message.h"
#include "../../common/sandboxd.h"
#include "../../common/socket.h"
#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>

bool is_loopback(const struct sockaddr *addr, socklen_t len) {
    (void)len;
    if (addr->sa_family == AF_INET) {
        const struct sockaddr_in *sin = (const struct sockaddr_in *)addr;
        return (ntohl(sin->sin_addr.s_addr) >> 24) == 127;
    }
    if (addr->sa_family == AF_INET6) {
        const struct sockaddr_in6 *sin6 = (const struct sockaddr_in6 *)addr;
        return memcmp(&sin6->sin6_addr, &in6addr_loopback, sizeof(struct in6_addr)) == 0;
    }
    return false;
}

int connect(int fd, const struct sockaddr *addr, socklen_t len) {
    static int (*real_connect)(int, const struct sockaddr *, socklen_t);
    if (!real_connect)
        real_connect = (int (*)(int, const struct sockaddr *, socklen_t))dlsym(RTLD_NEXT,
                                                                                "connect");
    if (!real_connect) {
        errno = ENOSYS;
        return -1;
    }
    if (is_loopback(addr, len))
        return real_connect(fd, addr, len);

    char host[NI_MAXHOST];
    char port[NI_MAXSERV];
    if (getnameinfo(addr, len, host, sizeof(host), port, sizeof(port),
                    NI_NUMERICHOST | NI_NUMERICSERV) != 0) {
        errno = EACCES;
        return -1;
    }

    if (ensure_daemon() < 0) {
        errno = EACCES;
        return -1;
    }

    char req[512];
    snprintf(req, sizeof(req), "%s %d %s %s", SANDBOXD_CMD_CONNECT, getpid(), host, port);
    if (sb_send_line_fd(daemon_fd, req, -1) < 0) {
        errno = EACCES;
        return -1;
    }

    char resp[256];
    int tun_fd = -1;
    if (sb_recv_line_fd(daemon_fd, resp, sizeof(resp), &tun_fd) < 0 ||
        strncmp(resp, SANDBOXD_RESP_OK, strlen(SANDBOXD_RESP_OK)) != 0 || tun_fd < 0) {
        if (tun_fd >= 0)
            close(tun_fd);
        errno = EACCES;
        return -1;
    }

    int fl = fcntl(fd, F_GETFL, 0);
    int fdflags = fcntl(fd, F_GETFD, 0);
    if (close(fd) < 0) {
        close(tun_fd);
        errno = EACCES;
        return -1;
    }
    if (dup2(tun_fd, fd) < 0) {
        close(tun_fd);
        errno = EACCES;
        return -1;
    }
    close(tun_fd);
    if (fl >= 0)
        fcntl(fd, F_SETFL, fl);
    if (fdflags >= 0)
        fcntl(fd, F_SETFD, fdflags);
    return 0;
}