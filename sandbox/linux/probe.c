#define _GNU_SOURCE
#include "../common/message.h"
#include "../common/sandboxd.h"
#include "../common/socket.h"
#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

static void die(const char *s) {
    perror(s);
    exit(127);
}

static void map_ids(void) {
    int fd = open("/proc/self/setgroups", O_WRONLY);
    if (fd >= 0) {
        write(fd, "deny\n", 5);
        close(fd);
    }
    char b[64];
    snprintf(b, sizeof b, "0 %ld 1\n", (long)getuid());
    fd = open("/proc/self/uid_map", O_WRONLY);
    if (fd < 0)
        die("uid_map");
    if (write(fd, b, strlen(b)) < 0)
        die("uid_map");
    close(fd);
    snprintf(b, sizeof b, "0 %ld 1\n", (long)getgid());
    fd = open("/proc/self/gid_map", O_WRONLY);
    if (fd < 0)
        die("gid_map");
    if (write(fd, b, strlen(b)) < 0)
        die("gid_map");
    close(fd);
}

static void mkdirs(const char *p) {
    char x[4096];
    snprintf(x, sizeof x, "%s", p);
    for (char *q = x + 1; *q; q++)
        if (*q == '/') {
            *q = 0;
            mkdir(x, 0755);
            *q = '/';
        }
    mkdir(x, 0755);
}

static void bind_one(const char *s, const char *d, int ro, const char *r) {
    char dst[4096], par[4096];
    struct stat st;
    if (stat(s, &st) < 0)
        die(s);
    snprintf(dst, sizeof dst, "%s%s", r, d);
    if (S_ISDIR(st.st_mode)) {
        mkdirs(dst);
    } else {
        snprintf(par, sizeof par, "%s", dst);
        char *q = strrchr(par, '/');
        if (q) {
            *q = 0;
            mkdirs(par);
        }
        int f = open(dst, O_CREAT | O_WRONLY, 0644);
        if (f >= 0)
            close(f);
    }
    if (mount(s, dst, NULL, MS_BIND, NULL) < 0)
        die("bind");
    if (ro && mount(NULL, dst, NULL, MS_BIND | MS_REMOUNT | MS_RDONLY, NULL) < 0)
        die("readonly bind");
}

static void register_pid(const char *p, pid_t pid) {
    int fd = sb_connect(p);
    if (fd < 0)
        return;
    char b[80];
    snprintf(b, sizeof b, "%s %d %d", SANDBOXD_CMD_REGISTER, (int)pid, (int)pid);
    msg_send_line(fd, b);
    close(fd);
}

int main(int ac, char **av) {
    const char *root, *sock = getenv("SANDBOX_DAEMON_SOCKET");
    int i = 1;
    if (ac < 4 || strcmp(av[i], "--root")) {
        fprintf(stderr, "usage: probe --root DIR [--bind SRC DST ro|rw]... -- command\n");
        return 2;
    }
    root = av[++i];
    for (i++; i < ac && strcmp(av[i], "--");) {
        if (i + 3 >= ac || strcmp(av[i], "--bind"))
            return 2;
        i += 4;
    }
    if (i >= ac || i + 1 >= ac)
        return 2;
    if (unshare(CLONE_NEWUSER | CLONE_NEWNS | CLONE_NEWUTS | CLONE_NEWIPC | CLONE_NEWNET | CLONE_NEWPID) < 0)
        die("unshare");
    map_ids();
    if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) < 0)
        die("private mounts");
    mkdirs(root);
    for (int j = 3; j < i; j += 4)
        bind_one(av[j + 1], av[j + 2], !strcmp(av[j + 3], "ro"), root);
    pid_t child = fork();
    if (child < 0)
        die("fork");
    if (child == 0) {
        if (chroot(root) < 0 || chdir("/") < 0)
            die("chroot");
        mkdir("/proc", 0555);
        if (mount("proc", "/proc", "proc", 0, NULL) < 0)
            die("proc");
        if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0)
            die("no_new_privs");
        execvp(av[i + 1], &av[i + 1]);
        die("exec");
    }
    if (sock)
        register_pid(sock, child);
    int st;
    waitpid(child, &st, 0);
    return WIFEXITED(st) ? WEXITSTATUS(st) : 128 + WTERMSIG(st);
}