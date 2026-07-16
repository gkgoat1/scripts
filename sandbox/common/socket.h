/* Unix socket helpers, including SCM_RIGHTS file-descriptor passing. */
#ifndef SANDBOX_COMMON_SOCKET_H
#define SANDBOX_COMMON_SOCKET_H

#include <stddef.h>
#include <sys/types.h>

/* Connect to a Unix domain socket at path. Returns fd or -1. */
int sb_connect(const char *path);

/* Send a line, optionally passing fd via SCM_RIGHTS. fd < 0 means no fd. */
int sb_send_line_fd(int sock, const char *line, int fd);

/* Receive a line and an optional fd via SCM_RIGHTS. *fd_out is set to -1
 * if no fd was received. Returns bytes read (like msg_recv_line) or -1.
 */
ssize_t sb_recv_line_fd(int sock, char *buf, size_t size, int *fd_out);

#endif /* SANDBOX_COMMON_SOCKET_H */