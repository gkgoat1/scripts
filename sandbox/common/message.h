/* Line-oriented message helpers for the sandbox daemon Unix socket. */
#ifndef SANDBOX_COMMON_MESSAGE_H
#define SANDBOX_COMMON_MESSAGE_H

#include <stddef.h>
#include <sys/types.h>

/* Send a newline-terminated line over fd. Returns 0 on success, -1 on error. */
int msg_send_line(int fd, const char *line);

/* Receive a newline-terminated line into buf. The newline is included.
 * Returns the number of bytes stored (excluding the terminating NUL) or -1.
 */
ssize_t msg_recv_line(int fd, char *buf, size_t size);

#endif /* SANDBOX_COMMON_MESSAGE_H */