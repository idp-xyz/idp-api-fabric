#!/usr/bin/env python3
"""Static verification for F03 design artifacts; never implies business test PASS."""
from pathlib import Path
import csv,re
from urllib.parse import unquote
from bs4 import BeautifulSoup
ROOT=Path(__file__).resolve().parents[1]
DOC=ROOT/'docs/F03_Domain_and_Data_Model.md'
HTML=ROOT/'docs/F03_Domain_and_Data_Model.html'
INDEX=ROOT/'index.html'
errors=[]
def check(condition,label):
 if not condition:errors.append(label)
text=DOC.read_text('utf-8');html=HTML.read_text('utf-8')
soup=BeautifulSoup(html,'html.parser')
headers=re.findall(r'^##\s+(\d+)\.',text,re.M)
check(headers==[str(i) for i in range(20)],f'F03 chapter numbers incorrect: {headers}')
fig=re.findall(r'<figure\s+class="architecture-figure"',text)
check(len(fig)==10,f'expected 10 figures, got {len(fig)}')
check(len(soup.select('figure.architecture-figure'))==10,'rendered figure count differs')
check(len(soup.find_all('h1'))==1,'HTML must have exactly one semantic H1')
check('PublicationSlot' in text and 'PublicationTarget' in text,'publication grain not explicit')
check(all(f'INV-{i:02}' in text for i in range(1,14)),'missing invariant ID')
check(all(f'DM-{i:02}' in text for i in range(1,19)),'missing scenario ID')
check('ADR-0004' in text and 'OD-03' in text,'unreviewed scope clarification not noted')
check('DESIGN_EXAMPLE' in text and 'NOT_RUN' in text,'missing design example/verification caveats')
ids=[tag.get('id') for tag in soup.select('[id]')]
check(len(ids)==len(set(ids)),'HTML duplicate IDs')
for tag in soup.select('a[href^="#"]'):
 href=tag.get('href')
 if href!='#':check(href[1:] in ids,f'Broken local anchor {href}')
for path in [HTML,INDEX]:
 document=BeautifulSoup(path.read_text('utf-8'),'html.parser')
 for a in document.select('a[href]'):
  href=a.get('href','')
  if not href or href.startswith(('https:','http:','mailto:','#','javascript:')):continue
  relative=unquote(href.split('#',1)[0].split('?',1)[0])
  if relative:check((path.parent/relative).is_file(),f'Broken local link in {path.name}: {href}')

def loadcsv(rel):
 with (ROOT/rel).open(encoding='utf-8-sig',newline='') as f:return list(csv.DictReader(f))
model=loadcsv('docs/F03_Logical_Model.csv')
trace=loadcsv('tests/acceptance/F03_domain_trace.csv')
scenes=loadcsv('tests/acceptance/F03_domain_scenarios.csv')
check(len(model)==37,'logic model count differs')
check(len({x['Entity'] for x in model})==37,'duplicate logical entity names')
check(len(trace)==89,'requirement trace must cover 89')
reqs={f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}
check({x['requirement_id'] for x in trace}==reqs,'trace missing/extra requirement ID')
check(all(x['verification_status']=='NOT_RUN' for x in trace),'trace falsely reports execution')
check(all(x['model_refs'].strip() and x['next_documents'].strip() for x in trace),'trace has empty design mapping')
check(len(scenes)==18 and {x['scenario_id'] for x in scenes}=={f'DM-{i:02}' for i in range(1,19)},'scenario count/ids invalid')
check(all(x['execution_status']=='NOT_RUN' for x in scenes),'scenario falsely reports execution')
check('F03_Domain_and_Data_Model.html' in INDEX.read_text('utf-8'),'index has no F03 link')
check('F03' in (ROOT/'docs/README.md').read_text('utf-8'),'docs index missing F03')
# F08 now exists; only require its proper Markdown/HTML pair.
check((ROOT/'docs/F08_Delivery_and_Acceptance.md').is_file() and (ROOT/'docs/F08_Delivery_and_Acceptance.html').is_file(),'F08 documented phase missing')
if errors:raise SystemExit('FAIL F03 static verification:\n'+'\n'.join(sorted(set(errors))))
print(f'PASS F03 static: chapters={len(headers)} figures={len(fig)} logical_entities={len(model)} trace={len(trace)} scenarios={len(scenes)}; no business tests executed.')
