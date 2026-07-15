#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <sys/types.h>
#include <mach-o/dyld.h>

extern char **environ;
typedef int (*execve_fn)(const char *, char *const[], char *const[]);
static execve_fn real_execve; static int resolving;
static int daemon_fd=-1; static pid_t registered_pid;
static const char *rawenv(const char *name) { static char *(*fn)(const char *); if (!fn) fn=(char *(*)(const char *))dlsym(RTLD_NEXT,"getenv"); return fn ? fn(name) : NULL; }
static void connect_daemon(void){const char*p=rawenv("SANDBOX_DAEMON_SOCKET");if(!p)return;daemon_fd=socket(AF_UNIX,SOCK_STREAM,0);if(daemon_fd<0)return;fcntl(daemon_fd,F_SETFD,FD_CLOEXEC);struct sockaddr_un a={.sun_family=AF_UNIX};snprintf(a.sun_path,sizeof a.sun_path,"%s",p);if(connect(daemon_fd,(void*)&a,sizeof a)<0){close(daemon_fd);daemon_fd=-1;return;}registered_pid=getpid();char b[256];snprintf(b,sizeof b,"REGISTER %d %d %s\n",registered_pid,getppid(),rawenv("_")?rawenv("_"):"unknown");write(daemon_fd,b,strlen(b));char r[16];read(daemon_fd,r,sizeof r);}
static void init_real(void){if(!real_execve&&!resolving){resolving=1;real_execve=(execve_fn)dlsym(RTLD_NEXT,"execve");resolving=0;}if(daemon_fd<0)connect_daemon();}
static bool ask(const char *request,const char *yes){if(daemon_fd<0)connect_daemon();if(daemon_fd<0)return false;char b[4096];snprintf(b,sizeof b,"%s\n",request);if(write(daemon_fd,b,strlen(b))<0){close(daemon_fd);daemon_fd=-1;return false;}ssize_t n=read(daemon_fd,b,sizeof b-1);if(n<=0)return false;b[n]=0;return !strncmp(b,yes,strlen(yes));}
static void child_register(void){if(daemon_fd>=0){close(daemon_fd);daemon_fd=-1;}connect_daemon();}
__attribute__((constructor)) static void sandbox_start(void){connect_daemon();}

int fork(void){static int(*real_fork)(void);if(!real_fork)real_fork=(int(*)(void))dlsym(RTLD_NEXT,"fork");int r=real_fork();if(r==0)child_register();return r;}
int execve(const char *path,char *const argv[],char *const envp[]){init_real();if(!real_execve||resolving){errno=ENOSYS;return -1;}char b[4096];snprintf(b,sizeof b,"OPEN %d %s",getpid(),path);(void)ask(b,"UPDATED");int r=real_execve(path,argv,envp);return r;}
int execv(const char *path,char *const argv[]){return execve(path,argv,environ);}

char *getenv(const char *name){static char*(*real_getenv)(const char*);if(!real_getenv)real_getenv=(char*(*)(const char*))dlsym(RTLD_NEXT,"getenv");if(!real_getenv)return NULL;const char *policy=real_getenv("SANDBOX_ENV_POLICY");if(policy&&strcmp(name,"SANDBOX_ENV_POLICY")&&strcmp(name,"SANDBOX_DAEMON_SOCKET")){char exe[1024];uint32_t size=sizeof(exe);if(_NSGetExecutablePath(exe,&size)==0){char req[2048];snprintf(req,sizeof req,"ENV %d %s %s",getpid(),exe,name);if(!ask(req,"ALLOW"))return "";}}return real_getenv(name);}

static bool allowed_path(const char*p){const char*b=strrchr(p,'/');b=b?b+1:p;if(b[0]=='.'&&strcmp(b,".")&&strcmp(b,".."))return !strcmp(b,".zshrc")||!strcmp(b,".bashrc")||!strcmp(b,".profile")||!strcmp(b,".bash_profile");return !strstr(p,"/Documents/")&&!strstr(p,"/Desktop/")&&!strstr(p,"/Downloads/");}
typedef int(*open_fn)(const char*,int,...);int open(const char*p,int f,...){static open_fn real; if(!real)real=(open_fn)dlsym(RTLD_NEXT,"open");if(!allowed_path(p)){errno=EACCES;return -1;}va_list a;va_start(a,f);mode_t m=(mode_t)va_arg(a,int);va_end(a);char req[4096];snprintf(req,sizeof req,"OPEN %d %s",getpid(),p);(void)ask(req,"UPDATED");return real(p,f,m);}