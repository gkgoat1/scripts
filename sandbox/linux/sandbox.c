#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sched.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <string.h>
#include <fcntl.h>

#define SOCKET_PATH "/tmp/sandbox_daemon.sock"

void send_to_daemon(const char* msg) {
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, SOCKET_PATH, sizeof(addr.sun_path) - 1);
    connect(fd, (struct sockaddr *)&addr, sizeof(addr));
    write(fd, msg, strlen(msg));
    close(fd);
}

int run_sandboxed(void *arg) {
    char **argv = (char **)arg;
    
    // Simplistic rule implementation:
    // We'd ideally mount a new root here.
    // For the demo, we just show we are in a new namespace.
    printf("[Linux Sandbox] Running in isolated namespace...\n");
    
    execvp(argv[0], argv);
    perror("execvp");
    return 1;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <cmd> [args...]\n", argv[0]);
        return 1;
    }

    // 1. Create namespaces
    // CLONE_NEWUSER: User namespace
    // CLONE_NEWNS: Mount namespace
    // CLONE_NEWUTS: UTS namespace (hostname)
    // CLONE_NEWPID: PID namespace
    int flags = CLONE_NEWUSER | CLONE_NEWNS | CLONE_NEWUTS | CLONE_NEWPID;
    
    char *stack = malloc(1024 * 1024);
    if (!stack) {
        perror("malloc");
        return 1;
    }

    pid_t pid = clone(run_sandboxed, stack + (1024 * 1024), flags | SIGC_SIGHANDLE, argv + 1);
    if (pid == -1) {
        perror("clone");
        return 1;
    }

    char reg_msg[64];
    snprintf(reg_msg, sizeof(reg_msg), "REG %d %s", pid, argv[1]);
    send_to_daemon(reg_msg);

    printf("Started sandboxed process %d\n", pid);
    return 0;
}