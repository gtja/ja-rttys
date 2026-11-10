import hashlib,json,os,pathlib,pwd,shutil,signal,stat,subprocess,tempfile

def emit(**data):
    print(CONFIG['marker']+json.dumps(data,ensure_ascii=True),flush=True)

def digest(fd):
    h=hashlib.sha256();os.lseek(fd,0,os.SEEK_SET)
    while True:
        b=os.read(fd,1024*1024)
        if not b:break
        h.update(b)
    os.lseek(fd,0,os.SEEK_SET)
    return h.hexdigest()

def stop(signum,frame):
    raise RuntimeError('传输已取消')

def main():
    user=pwd.getpwuid(os.geteuid())
    if user.pw_uid==0 or user.pw_name!=CONFIG['username']:
        raise ValueError('终端系统账号与运维账号不一致；请检查客户端 username 配置')
    root=pathlib.Path(CONFIG['root']).resolve(strict=True)
    target=(root/CONFIG['path']).resolve()
    if target!=root and root not in target.parents:raise ValueError('路径超出允许目录')
    if any(p.startswith('.rttys-transfers') for p in target.relative_to(root).parts):raise ValueError('不能访问临时传输目录')
    helper=shutil.which('rtty-tenant-poc') or shutil.which('rtty')
    if not helper:
        for name in ['/usr/local/bin/rtty-tenant-poc','/opt/rtty-tenant-poc/rtty']:
            if os.access(name,os.X_OK):helper=name;break
    if not helper:raise ValueError('设备未安装 rtty 文件传输助手')
    if CONFIG['direction']=='download':
        fd=os.open(str(target),os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
        try:
            before=os.fstat(fd)
            if not stat.S_ISREG(before.st_mode) or before.st_size>16*1024*1024:raise ValueError('仅支持不超过 16 MiB 的普通文件')
            checksum=digest(fd)
            emit(type='ready',size=before.st_size,name=target.name,sha256=checksum)
            r=subprocess.run([helper,'-S','/proc/self/fd/'+str(fd)],pass_fds=(fd,),stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            if r.returncode:raise ValueError(r.stderr.decode(errors='replace')[:300] or '文件下载中断')
            after=os.fstat(fd)
            if (before.st_size,before.st_mtime_ns)!=(after.st_size,after.st_mtime_ns):raise ValueError('下载期间文件发生变化')
            emit(type='complete',size=before.st_size,sha256=checksum)
        finally:os.close(fd)
    else:
        if target.exists() or target.is_symlink():raise ValueError('同名文件已存在，不会覆盖')
        if not target.parent.is_dir():raise ValueError('目标目录不存在')
        with tempfile.TemporaryDirectory(prefix='.rttys-transfers-',dir=str(target.parent)) as directory:
            emit(type='ready',size=CONFIG['size'],name=target.name)
            r=subprocess.run([helper,'-R'],cwd=directory,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            if r.returncode:raise ValueError(r.stderr.decode(errors='replace')[:300] or '文件上传中断')
            received=pathlib.Path(directory)/'payload'
            fd=os.open(str(received),os.O_RDONLY|os.O_NOFOLLOW)
            try:
                if os.fstat(fd).st_size!=CONFIG['size']:raise ValueError('上传文件大小不一致')
                checksum=digest(fd)
            finally:os.close(fd)
            if checksum!=CONFIG['sha256']:raise ValueError('上传文件 SHA-256 校验失败')
            os.link(str(received),str(target))
        emit(type='complete',size=CONFIG['size'],sha256=checksum)

signal.signal(signal.SIGHUP,stop)
signal.signal(signal.SIGTERM,stop)
try:main()
except Exception as e:emit(type='error',error=str(e)[:400])
