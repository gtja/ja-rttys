"""Executed by the existing rtty command protocol as a configured non-root user."""
import base64, errno, hashlib, json, os, pathlib, re, shutil, stat, subprocess, sys, tempfile, time

def main():
    if os.geteuid() == 0:
        raise ValueError('不允许以 root 执行文件管理或巡检')
    root = pathlib.Path(sys.argv[1]).resolve(strict=True)
    data = json.loads(sys.argv[2])
    action = data['action']
    def safe(value):
        p = (root / value).resolve()
        if p != root and root not in p.parents:
            raise ValueError('路径超出允许目录')
        if any(part.startswith('.rttys-transfers') for part in p.relative_to(root).parts):
            raise ValueError('这是保留的传输目录')
        return p
    def fingerprint(p):
        s=p.stat()
        return '{}:{}:{}'.format(s.st_ino,s.st_size,s.st_mtime_ns)
    if action == 'psdk_version':
        command = ['docker', 'inspect', '--type', 'container', 'ja-uav-psdk']
        result = subprocess.run(
            command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=6)
        if result.returncode and b'permission denied' in result.stderr.lower():
            result = subprocess.run(
                ['sudo', '-n'] + command,
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=6)
            if result.returncode:
                raise ValueError('执行账号无法读取 Docker，请允许免交互 sudo docker inspect')
        if result.returncode:
            raise ValueError('无法读取 ja-uav-psdk 容器，请检查 Docker 和容器名称')
        containers = json.loads(result.stdout)
        if len(containers) != 1 or not containers[0].get('State', {}).get('Running'):
            raise ValueError('ja-uav-psdk 容器当前未运行')
        container = containers[0]
        config = container.get('Config') or {}
        labels = config.get('Labels') or {}
        return dict(
            container='ja-uav-psdk',
            image=str(config.get('Image') or '')[:512],
            image_id=str(container.get('Image') or '')[:128],
            program_version=str(labels.get('org.opencontainers.image.version') or '')[:128])
    if action == 'list':
        path=safe(data.get('path','.')); entries=[]
        with os.scandir(str(path)) as scan:
            for entry in scan:
                if entry.name.startswith('.rttys-transfers'): continue
                if len(entries)>=300: raise ValueError('目录超过 300 项，请使用终端处理')
                info=entry.stat(follow_symlinks=False)
                entries.append(dict(name=entry.name,kind='link' if entry.is_symlink() else 'directory' if entry.is_dir(follow_symlinks=False) else 'file' if stat.S_ISREG(info.st_mode) else 'special',size=info.st_size,modified=int(info.st_mtime)))
        return dict(path=str(path.relative_to(root)),entries=sorted(entries,key=lambda x:(x['kind']!='directory',x['name'])))
    if action == 'read':
        path=safe(data['path']); before=fingerprint(path)
        if not path.is_file() or path.stat().st_size>16*1024*1024: raise ValueError('仅支持不超过 16 MiB 的普通文件')
        if data.get('stamp') and data['stamp']!=before: raise ValueError('下载期间文件发生变化，请重试')
        offset=int(data.get('offset',0))
        if offset<0 or offset>path.stat().st_size: raise ValueError('无效偏移')
        with path.open('rb') as f: f.seek(offset); chunk=f.read(16384)
        if fingerprint(path)!=before: raise ValueError('下载期间文件发生变化，请重试')
        return dict(data=base64.b64encode(chunk).decode(),size=path.stat().st_size,stamp=before,offset=offset)
    if action in ['begin','write','finish','cancel']:
        key=data['transfer']
        if not re.fullmatch('[a-f0-9]{64}',key): raise ValueError('无效传输编号')
        stage=(pathlib.Path(tempfile.gettempdir())/('.rttys-transfers-'+str(os.geteuid()))) if root==pathlib.Path('/') else root/'.rttys-transfers'
        if stage.is_symlink(): raise ValueError('无效传输目录')
        stage.mkdir(mode=0o700,exist_ok=True)
        if stage.stat().st_uid!=os.geteuid() or stage.stat().st_mode & 0o077: raise ValueError('传输目录权限不正确')
        part=stage/(key+'.part'); meta=stage/(key+'.json')
        if action=='begin':
            size=int(data['size'])
            if not 0<=size<=16*1024*1024: raise ValueError('上传文件上限为 16 MiB')
            target=safe(data['path'])
            if target.exists() or target.is_symlink(): raise ValueError('同名文件已存在，不会覆盖')
            if not target.parent.is_dir(): raise ValueError('目标目录不存在')
            if len(list(stage.glob('*.part')))>=20: raise ValueError('未完成传输过多，请取消后重试')
            with part.open('xb'): pass
            meta.write_text(json.dumps(dict(path=str(target.relative_to(root)),size=size)))
            return dict(ok=True)
        if action=='cancel':
            for p in [part,meta]:
                try:p.unlink()
                except FileNotFoundError:pass
            return dict(ok=True)
        record=json.loads(meta.read_text())
        if action=='write':
            chunk=base64.b64decode(data['data'],validate=True);offset=int(data['offset'])
            if len(chunk)>16384 or offset<0 or offset+len(chunk)>record['size']: raise ValueError('无效数据块')
            with part.open('r+b') as f:
                size=os.fstat(f.fileno()).st_size
                if offset>size: raise ValueError('上传数据不连续')
                f.seek(offset)
                if offset<size:
                    if f.read(len(chunk))!=chunk: raise ValueError('重试数据不一致')
                else:f.write(chunk);f.flush();os.fsync(f.fileno())
            return dict(offset=offset+len(chunk))
        if part.stat().st_size!=record['size']: raise ValueError('文件尚未上传完整')
        checksum=hashlib.sha256(part.read_bytes()).hexdigest()
        if checksum!=data['sha256']: raise ValueError('文件校验失败')
        target=safe(record['path'])
        try:
            os.link(str(part),str(target))  # Atomic no-overwrite commit.
        except OSError as e:
            if e.errno!=errno.EXDEV: raise
            # Preserve atomic no-overwrite publication across mounted filesystems.
            fd,temporary=tempfile.mkstemp(prefix='.rttys-transfers-',dir=str(target.parent))
            try:
                with os.fdopen(fd,'wb') as output,part.open('rb') as source:
                    shutil.copyfileobj(source,output);output.flush();os.fsync(output.fileno())
                os.link(temporary,str(target))
            finally:
                os.unlink(temporary)
        part.unlink();meta.unlink()
        return dict(ok=True,sha256=checksum)
    if action=='disk':
        s=os.statvfs(str(root));return dict(directory=str(root),total=s.f_blocks*s.f_frsize,available=s.f_bavail*s.f_frsize)
    if action in ['services','logs']:
        cmd=['systemctl','--no-pager','--plain','--failed'] if action=='services' else ['journalctl','--no-pager','-n','80','--output=short']
        result=subprocess.run(cmd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
        return dict(exit_code=result.returncode,stdout=result.stdout[:12000].decode(errors='replace'),stderr=result.stderr[:2000].decode(errors='replace'),truncated=len(result.stdout)>12000)
    raise ValueError('不支持的操作')

try:
    print(json.dumps(main(),ensure_ascii=True))
except Exception as e:
    print(json.dumps({'error':str(e)[:400]},ensure_ascii=True))
    sys.exit(1)
