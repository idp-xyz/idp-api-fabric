#!/usr/bin/env python3
"""Package one full V1.0 engineering baseline, verify CRC, SHA, and independent extraction.
Static file checks are not business, security, integration, release, or UAT test evidence.
"""
from pathlib import Path
from hashlib import sha256
from tempfile import TemporaryDirectory
import zipfile
import os
ROOT=Path(__file__).resolve().parents[1]
OUTPUT=ROOT.parent/'IDP_API_Fabric_Engineering_Baseline_V1.0_F01-F08.zip'
def tracked():
 return sorted((p for p in ROOT.rglob('*') if p.is_file() and p.name!='MANIFEST.sha256' and '__pycache__' not in p.parts and p.suffix not in ('.pyc','.pyo') and '.pytest_cache' not in p.parts),key=lambda p:p.relative_to(ROOT).as_posix())
files=tracked()
manifest=''.join(f'{sha256(p.read_bytes()).hexdigest()}  {p.relative_to(ROOT).as_posix()}\n' for p in files)
(ROOT/'MANIFEST.sha256').write_text(manifest,encoding='utf8')
allfiles=[ROOT/'MANIFEST.sha256']+files
with zipfile.ZipFile(OUTPUT,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=8) as z:
 for p in allfiles:
  z.write(p,arcname=(ROOT.name+'/'+p.relative_to(ROOT).as_posix()))
with zipfile.ZipFile(OUTPUT,'r') as z:
 corrupted=z.testzip()
 if corrupted:raise SystemExit(f'Invalid CRC for {corrupted}')
 expected={ROOT.name+'/'+p.relative_to(ROOT).as_posix() for p in allfiles}
 if set(z.namelist())!=expected:raise SystemExit('ZIP inventory differs from tracked files')
 with TemporaryDirectory() as tmp:
  z.extractall(tmp)
  extracted=Path(tmp)/ROOT.name
  # manifest itself stores SHA of each other file; verify all after extraction.
  checks=extracted.joinpath('MANIFEST.sha256').read_text('utf8').splitlines()
  for line in checks:
   digest,name=line.split('  ',1)
   p=(extracted/name).resolve()
   if not p.is_relative_to(extracted.resolve()) or not p.is_file() or sha256(p.read_bytes()).hexdigest()!=digest:
    raise SystemExit('Extracted file SHA mismatch: '+name)
  if len(checks)!=len(files):raise SystemExit('Missing file hashes in extracted manifest')
print('PACKAGE VERIFIED',OUTPUT,'total_zip_files',len(allfiles),'sha_checked',len(files),'size_bytes',OUTPUT.stat().st_size,'archive_sha256',sha256(OUTPUT.read_bytes()).hexdigest())
