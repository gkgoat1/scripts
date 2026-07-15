#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdbool.h>

// Simple rule engine mockup
bool is_allowed(const char* path) {
    // Default auto-deny TCC
    if (strstr(path, "/Documents") || strstr(path, "/Desktop") || strstr(path, "/Downloads")) {
        return false;
    }
    // Dotfiles
    if (path[0] == '.' || (strlen(path) > 0 && path[strlen(path)-1] == '/' && path[strlen(path)-2] == '.')) {
        // Shell configs Read-Only (simplified check)
        if (strstr(path, ".zshrc") || strstr(path, ".bashrc")) {
            return true; // Note: enforcement of RO would happen in the actual hook
        }
        return false;
    }
    return true;
}

// Intercepting 'open'
typedef int (*open_t)(const char*, int, mode_t);
int open(const char *path, int flags, mode_t mode) {
    static open_t real_open = NULL;
    if (!real_open) {
        real_open = (open_t)dlsym(RTLD_NEXT, "open");
    }

    if (!is_allowed(path)) {
        fprintf(stderr, "[Sandbox] Access Denied: %s\n", path);
        errno = EACCES;
        return -1;
    }

    return real_open(path, flags, mode);
}

// Intercepting 'unlink'
typedef int (*unlink_t)(const char*);
int unlink(const char *path) {
    static unlink_t real_unlink = NULL;
    if (!real_unlink) {
        real_unlink = (unlink_t)dlsym(RTLD_NEXT, "unlink");
    }

    if (!is_allowed(path)) {
        fprintf(stderr, "[Sandbox] Deletion Denied: %s\n", path);
        errno = EACCES;
        return -1;
    }

    return real_unlink(path);
}