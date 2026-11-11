#!/usr/bin/env python3
"""Package approved Linux client binaries with the project's public trust bundle."""
import argparse, hashlib, json, pathlib, shutil, ssl, tarfile, tempfile

p=argparse.ArgumentParser()
p.add_argument('--bin-dir',required=True,type=pathlib.Path,help='rtty-linux-amd64 and rtty-linux-arm64')
p.add_argument('--output',default='out/ja-rtty-client-linux.tar.gz',type=pathlib.Path)
a=p.parse_args()
repo=pathlib.Path(__file__).resolve().parent.parent
ca=repo/'certs/trusted-ca.pem'
assert b'PRIVATE KEY' not in ca.read_bytes(), 'Never ship private keys'
ssl.create_default_context(cafile=str(ca))
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
with tempfile.TemporaryDirectory(prefix='ja-rtty-package-') as temp:
    root=pathlib.Path(temp)/'ja-rtty-client-linux';(root/'bin').mkdir(parents=True)
    for arch,machine in [('amd64',62),('arm64',183)]:
        src=a.bin_dir/('rtty-linux-'+arch)
        header=src.read_bytes()[:20]
        assert header[:6]==b'\x7fELF\x02\x01' and int.from_bytes(header[18:20],'little')==machine, 'Incorrect client architecture: '+arch
        dest=root/'bin'/src.name;shutil.copy2(src,dest);dest.chmod(0o755)
    shutil.copy2(repo/'install-rtty',root/'install-rtty');(root/'install-rtty').chmod(0o755)
    for name in ['rtty-command.py', 'install-rtty-command']:
        shutil.copy2(repo/'scripts'/name, root/name)
        (root/name).chmod(0o755)
    shutil.copy2(ca,root/'cert.pem');(root/'cert.pem').chmod(0o644)
    shutil.copy2(repo/'LICENSE',root/'LICENSE')
    shutil.copy2(repo/'scripts/INSTALL-RTTY.md',root/'README.md')
    manifest={'ca_source':'certs/trusted-ca.pem','ca_install_path':'/etc/rtty/trusted-ca.pem','certificate_count':ca.read_bytes().count(b'-----BEGIN CERTIFICATE-----'),'files':{str(f.relative_to(root)):sha(f) for f in sorted(root.rglob('*')) if f.is_file()}}
    (root/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    (root/'SHA256SUMS').write_text(''.join(sha(f)+'  '+str(f.relative_to(root))+'\n' for f in sorted(root.rglob('*')) if f.is_file()))
    a.output.parent.mkdir(parents=True,exist_ok=True)
    with tarfile.open(a.output,'w:gz') as archive:archive.add(root,arcname=root.name)
a.output.with_suffix(a.output.suffix+'.sha256').write_text(sha(a.output)+'  '+a.output.name+'\n')
print(a.output.resolve())
