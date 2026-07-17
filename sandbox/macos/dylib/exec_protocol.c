#define _GNU_SOURCE
#include "exec_protocol.h"
#include "interpose.h"
#include "../../common/socket.h"
#include <errno.h>
#include <fcntl.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

struct writer { unsigned char *data; size_t len, cap; };
struct reader { const unsigned char *data; size_t len, off; int bad; };

static int reserve(struct writer *w, size_t add) {
    if (add > SBXP_MAX_FRAME || w->len > SBXP_MAX_FRAME - add) return -1;
    if (w->len + add <= w->cap) return 0;
    size_t cap = w->cap ? w->cap : 256;
    while (cap < w->len + add) cap *= 2;
    unsigned char *p = realloc(w->data, cap);
    if (!p) return -1;
    w->data = p; w->cap = cap; return 0;
}
static int put(struct writer *w, const void *p, size_t n) { if (reserve(w,n)<0) return -1; memcpy(w->data+w->len,p,n); w->len+=n; return 0; }
static int u8(struct writer *w, uint8_t v) { return put(w,&v,1); }
static int u32(struct writer *w, uint32_t v) { unsigned char b[4]={v>>24,v>>16,v>>8,v}; return put(w,b,4); }
static int u64(struct writer *w, uint64_t v) { unsigned char b[8]; for(int i=7;i>=0;i--){b[i]=(unsigned char)v;v>>=8;}return put(w,b,8); }
static int str(struct writer *w, const char *s) { size_t n=s?strlen(s):0; if(n>SBXP_MAX_STRING) return -1; return u32(w,(uint32_t)n)||put(w,s,n); }
static size_t count(char *const v[]) { size_t n=0; if(v) while(v[n]) { if(++n>SBXP_MAX_VECTOR) return 0; } return n; }
static int vec(struct writer *w, char *const v[]) { size_t n=count(v); if(v && n==0) return -1; if(u32(w,(uint32_t)n)<0) return -1; for(size_t i=0;i<n;i++) if(str(w,v[i])<0)return -1; return 0; }
static const unsigned char *take(struct reader *r,size_t n){if(r->bad||n>r->len-r->off){r->bad=1;return NULL;}const unsigned char*p=r->data+r->off;r->off+=n;return p;}
static uint8_t get8(struct reader*r){const unsigned char*p=take(r,1);return p?p[0]:0;}
static uint32_t get32(struct reader*r){const unsigned char*p=take(r,4);return p?((uint32_t)p[0]<<24)|((uint32_t)p[1]<<16)|((uint32_t)p[2]<<8)|p[3]:0;}
static uint64_t get64(struct reader*r){const unsigned char*p=take(r,8);uint64_t v=0;if(!p)return 0;for(int i=0;i<8;i++)v=(v<<8)|p[i];return v;}
static char *getstr(struct reader*r){uint32_t n=get32(r);if(n>SBXP_MAX_STRING){r->bad=1;return NULL;}const unsigned char*p=take(r,n);if(!p)return NULL;char*s=malloc((size_t)n+1);if(!s){r->bad=1;return NULL;}memcpy(s,p,n);s[n]=0;return s;}
static char **getvec(struct reader*r,size_t*out){uint32_t n=get32(r);if(n>SBXP_MAX_VECTOR){r->bad=1;return NULL;}char**v=calloc((size_t)n+1,sizeof(char*));if(!v){r->bad=1;return NULL;}for(uint32_t i=0;i<n;i++){v[i]=getstr(r);if(r->bad){for(uint32_t j=0;j<i;j++)free(v[j]);free(v);return NULL;}}*out=n;return v;}
static void freevec(char **v){if(!v)return;for(size_t i=0;v[i];i++)free(v[i]);free(v);}
static int write_all(int fd,const void*p,size_t n){const unsigned char*b=p;while(n){ssize_t r=write(fd,b,n);if(r<0){if(errno==EINTR)continue;return -1;}if(r==0)return -1;b+=r;n-=(size_t)r;}return 0;}
static int read_all(int fd,void*p,size_t n){unsigned char*b=p;while(n){ssize_t r=read(fd,b,n);if(r<0){if(errno==EINTR)continue;return -1;}if(r==0)return -1;b+=r;n-=(size_t)r;}return 0;}
static int frame_write(int fd,uint8_t typ,const unsigned char*p,size_t n){if(n+1>SBXP_MAX_FRAME)return-1;unsigned char h[4]={(n+1)>>24,(n+1)>>16,(n+1)>>8,n+1};return write_all(fd,h,4)||write_all(fd,&typ,1)||write_all(fd,p,n);}
static int frame_read(int fd,uint8_t*typ,unsigned char**out,size_t*n){unsigned char h[4];if(read_all(fd,h,4)<0)return-1;size_t z=((size_t)h[0]<<24)|((size_t)h[1]<<16)|((size_t)h[2]<<8)|h[3];if(z==0||z>SBXP_MAX_FRAME)return-1;unsigned char*b=malloc(z);if(!b)return-1;if(read_all(fd,b,z)<0){free(b);return-1;}*typ=b[0];*n=z-1;*out=b+1;return 0;}
static int send_magic(int fd){return write_all(fd,SBXP_MAGIC,4);}
static int recv_magic(int fd){char b[4];return read_all(fd,b,4)||memcmp(b,SBXP_MAGIC,4)?-1:0;}

static int op_result(int fd,uint64_t id,int ok,int code,const void*data,size_t len,const char*msg){struct writer w={0};int e=u64(&w,id)||u8(&w,ok?1:0)||u32(&w,(uint32_t)code)||u32(&w,(uint32_t)len)||put(&w,data,len)||str(&w,msg?msg:"");if(!e)e=frame_write(fd,SBXP_OPERATION_RESULT,w.data,w.len);free(w.data);return e;}
static int service_operation(int fd,const unsigned char*body,size_t len){struct reader r={body,len,0,0};uint64_t id=get64(&r);uint8_t kind=get8(&r);char*path=getstr(&r);size_t argc=0;char**argv=getvec(&r,&argc);char*dir=getstr(&r);size_t envc=0;char**env=getvec(&r,&envc);char*prompt=getstr(&r);int capture=get8(&r);(void)argc;(void)envc;if(r.bad||r.off!=r.len){free(path);freevec(argv);free(dir);freevec(env);free(prompt);return -1;}if(kind==SBXP_OP_RUN){if(!path||!dir||!argv||!env||strchr(path,'\n')){op_result(fd,id,0,1,NULL,0,"invalid approved operation");}else{int pipefd[2]={-1,-1};posix_spawn_file_actions_t act;posix_spawn_file_actions_init(&act);if(posix_spawn_file_actions_addchdir_np(&act,dir)!=0){posix_spawn_file_actions_destroy(&act);op_result(fd,id,0,1,NULL,0,"invalid approved working directory");free(path);freevec(argv);free(dir);freevec(env);free(prompt);return 0;}if(capture&&pipe(pipefd)==0){posix_spawn_file_actions_adddup2(&act,pipefd[1],STDOUT_FILENO);posix_spawn_file_actions_addclose(&act,pipefd[0]);}pid_t child;int rc=posix_spawn(&child,path,&act,NULL,argv,env);posix_spawn_file_actions_destroy(&act);if(pipefd[1]>=0)close(pipefd[1]);if(rc!=0){if(pipefd[0]>=0)close(pipefd[0]);op_result(fd,id,0,1,NULL,0,"approved spawn failed");}else{unsigned char*out=NULL;size_t used=0,cap=0;if(pipefd[0]>=0){unsigned char temp[4096];ssize_t n;while((n=read(pipefd[0],temp,sizeof(temp)))>0){if(used+(size_t)n>SBXP_MAX_STRING)break;if(used+(size_t)n>cap){cap=(used+(size_t)n)*2;out=realloc(out,cap);if(!out)break;}memcpy(out+used,temp,(size_t)n);used+=(size_t)n;}close(pipefd[0]);}int status=1;waitpid(child,&status,0);int code=WIFEXITED(status)?WEXITSTATUS(status):1;op_result(fd,id,1,code,out,used,"");free(out);}}}else if(kind==SBXP_OP_READ){FILE*f=path?fopen(path,"rb"):NULL;if(!f){op_result(fd,id,0,1,NULL,0,"guest read denied");}else{unsigned char*out=malloc(SBXP_MAX_STRING);size_t n=out?fread(out,1,SBXP_MAX_STRING,f):0;fclose(f);op_result(fd,id,1,0,out,n,"");free(out);}}else if(kind==SBXP_OP_CONFIRM){char pin[7];snprintf(pin,sizeof(pin),"%06u",arc4random_uniform(1000000));FILE*tty=fopen("/dev/tty","r+");if(!tty){op_result(fd,id,0,1,NULL,0,"no guest tty");}else{fprintf(tty,"%s: %s\nPIN: ",prompt?prompt:"Confirm",pin);char in[64]={0};fgets(in,sizeof(in),tty);fclose(tty);in[strcspn(in,"\r\n")]=0;op_result(fd,id,strcmp(in,pin)==0,0,NULL,0,strcmp(in,pin)==0?"":"confirmation did not match");}}else{op_result(fd,id,0,1,NULL,0,"unknown guest operation");}free(path);freevec(argv);free(dir);freevec(env);free(prompt);return 0;}

int sbxp_exec_authorize(const char *path,char *const argv[],char *const envp[],struct sbxp_exec_result *result){memset(result,0,sizeof(*result));if(!path||!argv||!argv[0]||ensure_daemon()<0)return-1;int fd=sb_connect(sandbox_rawenv("SANDBOX_DAEMON_SOCKET"));if(fd<0)return-1;if(send_magic(fd)<0||recv_magic(fd)<0){close(fd);return-1;}char cwd[4096];if(!getcwd(cwd,sizeof(cwd))){close(fd);return-1;}struct writer w={0};int bad=str(&w,path)||str(&w,cwd)||vec(&w,argv)||vec(&w,envp)||frame_write(fd,SBXP_EXEC_REQUEST,w.data,w.len);free(w.data);if(bad){close(fd);return-1;}for(;;){uint8_t typ;unsigned char*body;size_t len;if(frame_read(fd,&typ,&body,&len)<0){close(fd);return-1;}if(typ==SBXP_OPERATION_REQUEST){int rc=service_operation(fd,body,len);free(body);if(rc<0){close(fd);return-1;}continue;}if(typ!=SBXP_EXEC_RESULT){free(body);close(fd);return-1;}struct reader r={body,len,0,0};result->allowed=get8(&r);result->path=getstr(&r);result->argv=getvec(&r,&result->argc);result->env=getvec(&r,&result->envc);result->message=getstr(&r);free(body);close(fd);if(r.bad||r.off!=r.len){sbxp_free_exec_result(result);return-1;}return 0;}}
void sbxp_free_exec_result(struct sbxp_exec_result*r){if(!r)return;free(r->path);freevec(r->argv);freevec(r->env);free(r->message);memset(r,0,sizeof(*r));}