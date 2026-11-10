#!/usr/bin/env python3
"""Verify integrity of checked-in files against MANIFEST.sha256.
Run at project root: python scripts/verify_manifest.py.
Does not assess semantic correctness or project approval.
"""
from pathlib import Path
import hashlib
ROOT=Path(__file__).resolve().parents[1]
manifest=ROOT/'MANIFEST.sha256'
lines=manifest.read_text('utf8').splitlines()
errors=[]
for line in lines:
 try:
  expected,relative=line.split('  ',1)
  file=(ROOT/relative).resolve()
  if not file.is_relative_to(ROOT.resolve()) or not file.is_file():raise ValueError('missing/invalid path')
  actual=hashlib.sha256(file.read_bytes()).hexdigest()
  if actual!=expected:raise ValueError(f'digest mismatch expected={expected}, actual={actual}')
 except Exception as e:errors.append(f'{line}: {e}')
if errors:raise SystemExit('FAIL\n'+'\n'.join(errors))
print(f'PASS: {len(lines)} file hashes matched; this is file integrity verification only.')
