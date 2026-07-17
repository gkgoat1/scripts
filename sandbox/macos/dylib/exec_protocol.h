#include <stddef.h>
#include <stdint.h>
#include <sys/types.h>

#ifndef SANDBOX_MACOS_EXEC_PROTOCOL_H
#define SANDBOX_MACOS_EXEC_PROTOCOL_H

#define SBXP_MAGIC "SBX1"
#define SBXP_VERSION 1
#define SBXP_MAX_FRAME (1024 * 1024)
#define SBXP_MAX_STRING (64 * 1024)
#define SBXP_MAX_VECTOR 256

#define SBXP_EXEC_REQUEST 1
#define SBXP_EXEC_RESULT 2
#define SBXP_OPERATION_REQUEST 3
#define SBXP_OPERATION_RESULT 4

#define SBXP_OP_RUN 1
#define SBXP_OP_READ 2
#define SBXP_OP_CONFIRM 3

struct sbxp_exec_result {
    int allowed;
    char *path;
    char **argv;
    size_t argc;
    char **env;
    size_t envc;
    char *message;
};

struct sbxp_operation {
    uint64_t id;
    uint8_t kind;
    char *path;
    char **argv;
    size_t argc;
    char *dir;
    char **env;
    size_t envc;
    char *prompt;
    int capture;
};

int sbxp_exec_authorize(const char *path, char *const argv[], char *const envp[],
                        struct sbxp_exec_result *result);
void sbxp_free_exec_result(struct sbxp_exec_result *result);

#endif /* SANDBOX_MACOS_EXEC_PROTOCOL_H */