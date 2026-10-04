#!/usr/bin/env python3
from pathlib import Path
import subprocess,hashlib,json,sys
paths=sorted(set(subprocess.check_output(['git','ls-files','-c','-o','--exclude-standard','-z']).decode().split('\0'))-{''})
files=[{'path':p,'sha256':hashlib.sha256(Path(p).read_bytes()).hexdigest()} for p in paths if not p.startswith('.cursor/') and Path(p).is_file()]
digest=hashlib.sha256(''.join(f['path']+'\0'+f['sha256']+'\n' for f in files).encode()).hexdigest()
p=Path('.cursor/tasks/evidence/017/product-freeze.json')
if '--check' in sys.argv:
 x=json.loads(p.read_text());assert x['digest']==digest,(x['digest'],digest);assert x['files']==files;print('unchanged',digest)
else:
 p.write_text(json.dumps({'algorithm':'SHA256(sorted path+NUL+fileSHA256+LF)','excluded':'Task and evidence records under .cursor/ only; product, code, source docs, fixtures, scripts and CI are included.','digest':digest,'files':files},ensure_ascii=False,indent=2)+'\n');print('frozen',digest,len(files))
