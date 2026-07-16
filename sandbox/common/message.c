#include "message.h"
#include <errno.h>
#include <string.h>
#include <unistd.h>

int msg_send_line(int fd, const char *line) {
    size_t len = strlen(line);
    const char *p = line;
    while (len > 0) {
        ssize_t n = write(fd, p, len);
        if (n < 0) {
            if (errno == EINTR)
                continue;
            return -1;
        }
        p += (size_t)n;
        len -= (size_t)n;
    }
    while (1) {
        char nl = '\n';
        ssize_t n = write(fd, &nl, 1);
        if (n < 0) {
            if (errno == EINTR)
                continue;
            return -1;
        }
        if (n == 1)
            break;
    }
    return 0;
}

ssize_t msg_recv_line(int fd, char *buf, size_t size) {
    if (size == 0)
        return -1;
    size_t i = 0;
    while (i + 1 < size) {
        char ch;
        ssize_t n = read(fd, &ch, 1);
        if (n < 0) {
            if (errno == EINTR)
                continue;
            return -1;
        }
        if (n == 0)
            break;
        buf[i++] = ch;
        if (ch == '\n')
            break;
    }
    if (i == 0)
        return -1;
    buf[i] = '\0';
    return (ssize_t)i;
}