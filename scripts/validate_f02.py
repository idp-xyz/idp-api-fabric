#!/usr/bin/env python3
"""F02 design-document structural/traceability verification only.
This cannot constitute architecture acceptance, authorization, security, capacity, or runtime evidence.
"""
from pathlib import Path
from bs4 import BeautifulSoup
import csv,re,hashlib
ROOT=Path(__file__).resolve().parents[1]
md=(ROOT/'docs/F02_System_Architecture.md').read_text('utf8')
html=(ROOT/'docs/F02_System_Architecture.html').read_text('utf8')
soup=BeautifulSoup(html,'html.parser')
index=BeautifulSoup((ROOT/'index.html').read_text('utf8'),'html.parser')
errs=[]
if soup.select('h1') and len(soup.select('h1'))!=1:errs.append('F02 HTML should have exactly one H1')
ids=[x.get('id') for x in soup.select('[id]') if x.has_attr('id')]
if len(set(ids))!=len(ids):errs.append('Duplicate HTML id')
anchors=[a.get('href','')[1:] for a in soup.select('#toc a[href^="#"]')]
if len(anchors)<35:errs.append('Too few TOC entries')
for id_ in anchors:
 if id_ not in ids:errs.append('Broken nav anchor: '+id_)
fig=soup.select('figure.architecture-figure')
if len(fig)!=8:errs.append(f'Expected 8 figures, got {len(fig)}')
for i,f in enumerate(fig,1):
 if not f.select_one('.fig-label') or not f.find('figcaption'):
  errs.append(f'Figure {i} lacks descriptive title/caption')
for i in range(20):
 if f'## {i}. ' not in md:errs.append('Missing architecture chapter '+str(i))
if len(soup.select('table'))<18:errs.append('Unexpectedly sparse architecture document')
for f in (ROOT/'decisions').glob('ADR-*.md'):
 if 'Proposed' not in f.read_text('utf8'):
  errs.append('ADR not clearly proposed: '+f.name)
for i in range(1,7):
 if not (ROOT/'decisions'/f'ADR-{i:04d}.md').is_file():errs.append(f'Missing ADR-{i:04d}')
for loc in ['docs/F01_Product_Requirements.html','docs/F02_System_Architecture.html','docs/F01_Product_Requirements.md','docs/F02_System_Architecture.md']:
 if not (ROOT/loc).is_file():errs.append('Missing '+loc)
 if not index.find('a',href=loc):errs.append('Index lacks link: '+loc)
for anchor in soup.find_all('a',href=True):
 link=anchor['href']
 if not link or link.startswith(('https://','http://','mailto:','#','javascript:')):continue
 filepath=(ROOT/'docs'/link.split('#')[0].split('?')[0]).resolve()
 if not filepath.exists():errs.append('Missing local F02 link '+link)
with (ROOT/'tests/acceptance/F02_architecture_trace.csv').open(encoding='utf-8-sig',newline='') as f:
 trace=list(csv.DictReader(f))
expected=[f'FR-{i:03d}' for i in range(1,72)]+[f'NFR-{i:02d}' for i in range(1,19)]
found=[r['requirement_id'] for r in trace]
if found!=expected:errs.append('FR/NFR trace gaps, sequence or extra entries')
for r in trace:
 if not r['f02_sections'] or not r['architecture_decisions'] or not r['subsequent_documents']:errs.append('Incomplete trace '+r['requirement_id'])
 if r['verification_status']!='NOT_RUN':errs.append('Invalid test status '+r['requirement_id'])
with (ROOT/'tests/acceptance/F02_architecture_scenarios.csv').open(encoding='utf-8-sig',newline='') as f:
 scenarios=list(csv.DictReader(f))
if [r['scenario_id'] for r in scenarios]!=[f'AR-{i:02d}' for i in range(1,13)]:errs.append('Architecture scenarios incomplete')
if any(r['execution_status']!='NOT_RUN' for r in scenarios):errs.append('Architecture scenarios falsely passed')
if (ROOT/'governance/policies/F01_Project_Manifest_DRAFT.json').read_text('utf8').find('DRAFT_NOT_APPROVED')<0:
 errs.append('Upstream manifest status is not draft')
if not index.select('.summary'):errs.append('Index missing overview')
print('F02 HTML:',len(soup.select('h2')),'chapters,',len(soup.select('h2,h3')),'navigation headings,',len(fig),'illustrated diagrams,',len(soup.select('table')),'tables')
print('ADR files:',len(list((ROOT/'decisions').glob('ADR-*.md'))),'(all proposed)')
print('Traceability:',len(trace),'FR/NFR entries;',len(scenarios),'architecture scenarios; all NOT_RUN')
if errs:raise SystemExit('FAIL: '+str(len(errs))+' issue(s)\n'+'\n'.join(errs))
print('PASS: F02 structural and design trace validation only; NOT business, security, WCAG, runtime, or governance gate approval')
