#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <string.h>
#include <signal.h>
#include <errno.h>

#define SOCKET_PATH "/tmp/sandbox_daemon.sock"

typedef struct {
    pid_t pid;
    char cmd[256];
} SandboxProcess;

SandboxProcess processes[1024];
int process_count = 0;

void handle_sigint(int sig) {
    printf("\nShutting down daemon and killing all sandboxes...\n");
    for (int i = 0; i < process_count; i++) {
        kill(processes[i].pid, SIGKILL);
    }
    unlink(SOCKET_PATH);
    exit(0);
}

void register_process(pid_t pid, const char* cmd) {
    if (process_count < 1024) {
        processes[process_count].pid = pid;
        strncpy(processes[process_count].cmd, cmd, 255);
        process_count++;
    }
}

void terminate_process(pid_t pid) {
    kill(pid, SIGKILL);
}

int main() {
    signal(SIGINT, handle_sigint);

    int server_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (server_fd == -1) {
        perror("socket");
        exit(1);
    }

    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, SOCKET_PATH, sizeof(addr.sun_path) - 1);
    unlink(SOCKET_PATH);

    if (bind(server_fd, (struct sockaddr *)&addr, sizeof(addr)) == -1) {
        perror("bind");
        exit(1);
    }

    listen(server_fd, 5);
    printf("Sandbox Daemon running. Socket: %s\n", SOCKET_PATH);

    while (1) {
        int client_fd = accept(server_fd, NULL, NULL);
        if (client_fd == -1) continue;

        char buffer[512];
        int n = read(client_fd, buffer, sizeof(buffer) - 1);
        if (n > 0) {
            buffer[n] = '\0';
            if (strncmp(buffer, "REG ", 4) == 0) {
                pid_t pid;
                char cmd[256];
                sscanf(buffer + 4, "%d %s", &pid, cmd);
                register_process(pid, cmd);
                write(client_fd, "OK\n", 3);
            } else if (strncmp(buffer, "KILL ", 5) == 0) {
                pid_t pid;
                sscanf(buffer + 5, "%d", &pid);
                terminate_process(pid);
                write(client_fd, "OK\n", 3);
            } else if (strncmp(buffer, "KILLALL", 7) == 0) {
                for (int i = 0; i < process_count; i++) {
                    kill(processes[i].pid, SIGKILL);
                }
                write(client_fd, "OK\n", 3);
            }
        }
        close(client_fd);
    }
    return 0;
}