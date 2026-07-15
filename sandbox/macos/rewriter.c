#include <mach-o/loader.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

static void fail(const char *s){perror(s);exit(1);}
int main(int argc,char **argv){
 if(argc!=4){fprintf(stderr,"usage: rewriter INPUT OUTPUT DYLIB\n");return 2;}
 int fd=open(argv[1],O_RDONLY);if(fd<0)fail(argv[1]);struct stat st;if(fstat(fd,&st)<0)fail("stat");
 unsigned char *in=malloc(st.st_size);if(!in||read(fd,in,st.st_size)!=st.st_size)fail("read");close(fd);
 struct mach_header_64 *h=(void*)in;if(h->magic!=MH_MAGIC_64){fprintf(stderr,"only thin 64-bit Mach-O is supported\n");return 2;}
 size_t oldend=sizeof(*h)+h->sizeofcmds;size_t nlen=strlen(argv[3])+1;size_t cmdsz=(sizeof(struct dylib_command)+nlen+7)&~7ULL;
 size_t firstoff=(size_t)-1;struct load_command *lc=(void*)(in+sizeof *h);for(uint32_t i=0;i<h->ncmds;i++){if(lc->cmdsize<8||((unsigned char*)lc+lc->cmdsize)>in+st.st_size) return 2; if(lc->cmd==LC_SEGMENT_64){struct segment_command_64 *s=(void*)lc;if(s->fileoff>0&&s->fileoff<firstoff)firstoff=s->fileoff;}lc=(void*)((char*)lc+lc->cmdsize);}if(firstoff==(size_t)-1||firstoff<oldend){fprintf(stderr,"no safe Mach-O load-command padding; refusing rewrite\n");return 3;}
 size_t available=firstoff-oldend;if(available<cmdsz){fprintf(stderr,"no Mach-O load-command padding; refusing unsafe rewrite\n");return 3;}
 unsigned char *out=malloc(st.st_size);memcpy(out,in,st.st_size);memmove(out+oldend+cmdsz,out+oldend,st.st_size-oldend);memset(out+oldend,0,cmdsz);
 struct dylib_command *d=(void*)(out+oldend);d->cmd=LC_LOAD_DYLIB;d->cmdsize=cmdsz;d->dylib.name.offset=sizeof(*d);d->dylib.timestamp=2;d->dylib.current_version=0x10000;d->dylib.compatibility_version=0x10000;memcpy((char*)d+d->dylib.name.offset,argv[3],nlen);
 struct mach_header_64 *oh=(void*)out;oh->ncmds++;oh->sizeofcmds+=cmdsz;int ofd=open(argv[2],O_CREAT|O_TRUNC|O_WRONLY,0755);if(ofd<0)fail(argv[2]);if(write(ofd,out,st.st_size)!=st.st_size)fail("write");close(ofd);return 0;
}