/* Shared declarations for the macOS interposition dylib. */
#ifndef SANDBOX_MACOS_INTERPOSE_H
#define SANDBOX_MACOS_INTERPOSE_H

#include <fcntl.h>
#include <netdb.h>
#include <stdbool.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

/* Daemon connection. */
extern int daemon_fd;
extern pid_t registered_pid;

/* Ensure a connection to the daemon exists. Returns 0 on success, -1 on error. */
int ensure_daemon(void);

/* Re-register after fork(2). */
void child_register(void);

/* Policy helpers implemented by the dylib modules. */
bool open_allowed(const char *path, int flags);
bool is_loopback(const struct sockaddr *addr, socklen_t len);

#endif /* SANDBOX_MACOS_INTERPOSE_H */