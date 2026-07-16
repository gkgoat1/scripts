#define _GNU_SOURCE
#include "socket.h"
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

int sb_connect(const char *path) {
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0)
        return -1;
    if (fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) {
        close(fd);
        return -1;
    }
    struct sockaddr_un addr = {.sun_family = AF_UNIX};
    if (strlen(path) >= sizeof(addr.sun_path)) {
        close(fd);
        errno = ENAMETOOLONG;
        return -1;
    }
    strcpy(addr.sun_path, path);
    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }
    return fd;
}

int sb_send_line_fd(int sock, const char *line, int fd) {
    struct msghdr msg = {0};
    char nl = '\n';

    size_t len = strlen(line);
    /* We send the line plus a trailing newline as two iovecs. */
    struct iovec iovs[2];
    iovs[0].iov_base = (void *)line;
    iovs[0].iov_len = len;
    iovs[1].iov_base = &nl;
    iovs[1].iov_len = 1;
    msg.msg_iov = iovs;
    msg.msg_iovlen = 2;

    char control[CMSG_SPACE(sizeof(int))] = {0};
    if (fd >= 0) {
        msg.msg_control = control;
        msg.msg_controllen = sizeof(control);
        struct cmsghdr *cmsg = CMSG_FIRSTHDR(&msg);
        cmsg->cmsg_level = SOL_SOCKET;
        cmsg->cmsg_type = SCM_RIGHTS;
        cmsg->cmsg_len = CMSG_LEN(sizeof(int));
        memcpy(CMSG_DATA(cmsg), &fd, sizeof(int));
    }

    size_t total = len + 1;
    size_t sent = 0;
    while (sent < total) {
        ssize_t n = sendmsg(sock, &msg, 0);
        if (n < 0) {
            if (errno == EINTR)
                continue;
            return -1;
        }
        if (n == 0)
            return -1;
        sent += (size_t)n;
        if (sent >= total)
            break;
        /* Adjust iovec pointers for partial sends. */
        size_t skip = sent;
        msg.msg_iov = iovs;
        if (skip < len) {
            iovs[0].iov_base = (void *)(line + skip);
            iovs[0].iov_len = len - skip;
            iovs[1].iov_base = &nl;
            iovs[1].iov_len = 1;
            msg.msg_iovlen = 2;
        } else {
            iovs[0].iov_base = &nl;
            iovs[0].iov_len = 0;
            iovs[1].iov_base = &nl;
            iovs[1].iov_len = 1;
            msg.msg_iovlen = 1;
        }
    }
    return 0;
}

ssize_t sb_recv_line_fd(int sock, char *buf, size_t size, int *fd_out) {
    if (size == 0 || fd_out == NULL) {
        errno = EINVAL;
        return -1;
    }
    *fd_out = -1;

    char control[CMSG_SPACE(sizeof(int))] = {0};
    struct msghdr msg = {0};
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = size - 1;
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;
    msg.msg_control = control;
    msg.msg_controllen = sizeof(control);

    size_t i = 0;
    while (i + 1 < size) {
        iov.iov_base = buf + i;
        iov.iov_len = size - 1 - i;
        msg.msg_control = control;
        msg.msg_controllen = sizeof(control);

        ssize_t n = recvmsg(sock, &msg, 0);
        if (n < 0) {
            if (errno == EINTR)
                continue;
            return -1;
        }
        if (n == 0)
            break;

        /* Extract the first SCM_RIGHTS fd we see. */
        if (*fd_out < 0) {
            for (struct cmsghdr *cmsg = CMSG_FIRSTHDR(&msg); cmsg != NULL;
                 cmsg = CMSG_NXTHDR(&msg, cmsg)) {
                if (cmsg->cmsg_level == SOL_SOCKET && cmsg->cmsg_type == SCM_RIGHTS &&
                    cmsg->cmsg_len >= CMSG_LEN(sizeof(int))) {
                    memcpy(fd_out, CMSG_DATA(cmsg), sizeof(int));
                    break;
                }
            }
        }

        /* Find newline in the bytes just received. */
        size_t start = i;
        i += (size_t)n;
        for (size_t k = start; k < i; k++) {
            if (buf[k] == '\n') {
                buf[i] = '\0';
                return (ssize_t)i;
            }
        }
    }
    if (i == 0)
        return -1;
    buf[i] = '\0';
    return (ssize_t)i;
}