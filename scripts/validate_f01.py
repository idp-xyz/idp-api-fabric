#!/usr/bin/env python3
"""F01 static source, traceability, generated HTML and directory integrity.
Does NOT perform normative SCG semantic adoption checks, product tests or sign-off.
"""
from __future__ import annotations
import csv
import json
import re
from collections import Counter
from pathlib import Path
from bs4 import BeautifulSoup

ROOT = Path(__file__).resolve().parents[1]
md=(ROOT/'docs/F01_Product_Requirements.md').read_text('utf-8')
html=(ROOT/'docs/F01_Product_Requirements.html').read_text('utf-8')
soup=BeautifulSoup(html,'html.parser')
errors=[]
ids_by_type={}
for p,width in [('FR',3),('NFR',2),('AT',2),('BG',2),('UC',2)]:
    regex=(rf'^\| ({p}-\d{{{width}}})(?:\s+[^|]*)? \|' if p=='UC' else rf'^\| ({p}-\d{{{width}}}) \|')
    nums=re.findall(regex,md,re.M)
    vals=[int(x.rsplit('-',1)[1]) for x in nums]
    if vals!=list(range(1,len(vals)+1)) or len(vals)!=len(set(vals)):
        errors.append(f'{p}: non-contiguous or duplicate ID / bad sequence: {nums}')
    if not vals:errors.append(f'{p}: no IDs found')
    ids_by_type[p]=set(nums)
    print(f'{p}: {len(nums)} contiguous stable IDs')
if len(soup.select('h1'))!=1:errors.append('Document must have a single semantic H1')
all_ids=[x.get('id') for x in soup.select('[id]') if x.get('id')]
for name,n in Counter(all_ids).items():
    if n>1:errors.append('Duplicate html id '+name)
for a in soup.select('#toc a[href^="#"]'):
    if a.get('href')[1:] not in all_ids:errors.append('Broken TOC anchor '+a.get('href'))
for name in ['FABRIC_OVERVIEW_DIAGRAM','FABRIC_RELATIONSHIP_DIAGRAM','FABRIC_CAPABILITY_DIAGRAM','FABRIC_JOURNEY_DIAGRAM','FABRIC_ROADMAP_DIAGRAM','FABRIC_LIFECYCLE_DIAGRAM','FABRIC_PORTALS_DIAGRAM','FABRIC_TRACE_DIAGRAM']:
    if name in html:errors.append('Unexpanded diagram marker '+name)
if len(soup.select('figure'))!=8:errors.append('Expected 8 meaningful figures')
if len(soup.select('.table-scroll table'))!=len(soup.select('table')):errors.append('Missing accessible responsive table wrapper')
if not soup.select_one('#sideBackdrop') or not soup.select_one('#main-content'):
    errors.append('Mobile nav/skip content elements missing')
for file in ['README.md','index.html','docs/README.md','decisions/ADR-0001.md','scripts/build_docs.py','scripts/generate_trace.py','governance/policies/F01_Project_Manifest_DRAFT.json','tests/acceptance/F01_requirement_trace.csv']:
    if not (ROOT/file).exists():errors.append('Missing '+file)
for element in soup.find_all('a',href=True):
    href=element['href']
    if href.startswith(('http:','https:','mailto:','#','javascript:')):continue
    local=(ROOT/'docs'/href.split('#',1)[0].split('?',1)[0]).resolve()
    if not local.exists(): errors.append('Broken local doc link '+href)
with (ROOT/'tests/acceptance/F01_requirement_trace.csv').open(encoding='utf-8',newline='') as fp:
    trace=list(csv.DictReader(fp))
found=Counter(r['requirement_id'] for r in trace)
expected=ids_by_type['FR']|ids_by_type['NFR']
if set(found)!=expected:errors.append(f'Trace missing {sorted(expected-set(found))}; extra {sorted(set(found)-expected)}')
for k,v in found.items():
    if v!=1:errors.append(f'Duplicate trace entry {k} repeated {v}')
for record in trace:
    r=record['requirement_id']
    if record['evidence_status']!='NOT_RUN':errors.append('Fabricated executed evidence status in '+r)
    if record['trace_status']!='DESIGN_LINKS_ONLY':errors.append('Invalid design-only trace status in '+r)
    if not record['business_goals'] or not record['acceptance_scenarios']:
        errors.append('Missing business/acceptance trace input '+r)
    for code in record['business_goals'].split(';'):
        if code not in ids_by_type['BG']:errors.append(f'{r} invalid BG {code}')
    for code in record['acceptance_scenarios'].split(';'):
        if code!='F08-TBD' and code not in ids_by_type['AT']:
            errors.append(f'{r} invalid AT {code}')
    if r.startswith('FR-'):
        match=re.search(r'^\| '+re.escape(r)+r' \| [^|]+ \| ([^|]+) \|$',md,re.M)
        if not match or match.group(1).strip()!=record['phase']:
            errors.append('Trace stage/source mismatch '+r)
manifest=json.loads((ROOT/'governance/policies/F01_Project_Manifest_DRAFT.json').read_text('utf-8'))
if manifest.get('adoption',{}).get('status')!='DRAFT_NOT_APPROVED':
    errors.append('Project Manifest is incorrectly marked as formally approved')
if any(v!='NOT_RUN' for v in manifest.get('gate_records',{}).values()):
    errors.append('Gate incorrectly marked executed/passed')
if '正式设计定稿（待授权签收）' not in md:errors.append('F01 content status is not aligned')
if '正式设计定稿（待授权签收）' not in md or 'V1.0' not in html:errors.append('Wrong declared document version/status')
print('HTML:',len(soup.select('h2,h3')),'sections,',len(soup.select('table')),'tables,',len(soup.select('figure')),'diagrams')
print('Traceability:',len(trace),'design-stage entries, all NOT_RUN')
if errors:
    raise SystemExit('FAIL:\n'+'\n'.join(errors))
print('PASS: static F01 consistency only; NOT formal project adoption, WCAG, integration or user acceptance')
