#!/usr/bin/env python3
"""Verify F06 design artifacts structurally. Business/security/a11y acceptance remains NOT_RUN."""
from pathlib import Path
from bs4 import BeautifulSoup
import csv,re,subprocess,tempfile
from urllib.parse import unquote
ROOT=Path(__file__).resolve().parents[1];D=ROOT/'docs';T=ROOT/'tests/acceptance';P=ROOT/'portal/prototypes'
errors=[]
def chk(v,s):
 if not v:errors.append(s)
def rows(name):
 with (ROOT/name).open(encoding='utf8',newline='') as f:return list(csv.DictReader(f))
source=(D/'F06_Developer_Experience.md').read_text('utf8');page=(D/'F06_Developer_Experience.html').read_text('utf8');soup=BeautifulSoup(page,'html.parser')
chapters=re.findall(r'^##\s+(\d+)\.\s',source,re.M)
chk(chapters==[str(i) for i in range(21)],'F06 top-level chapters 0..20')
chk(source.count('<figure class="architecture-figure"')==8,'F06 Markdown diagram count')
chk(len(soup.select('figure.architecture-figure'))==8,'F06 HTML diagram count')
chk(len(soup.find_all('h1'))==1,'F06 one semantic H1')
chk(all(x in source for x in ('F06-A','F06-B','F06-C','PublicationSlot','AccessBlock','LOCAL_MOCK','NOT_RUN','WCAG 2.2 AA')),'F06 required concepts')
chk(all(x in source for x in ('IN-01','EX-09','SH-01')),'F06 page ID refs')
ids=[x.get('id') for x in soup.select('[id]')];chk(len(ids)==len(set(ids)),'duplicate HTML ids')
for a in soup.select('a[href^="#"]'):
 href=a.get('href');chk(href=='#' or href[1:] in ids,'bad F06 anchor '+href)
inventory=rows('docs/F06_Page_Inventory.csv');trace=rows('tests/acceptance/F06_experience_trace.csv');cases=rows('tests/acceptance/F06_experience_scenarios.csv')
chk(len(inventory)==21 and len({x['page_id'] for x in inventory})==21,'page inventory 21 unique')
chk(set(x['page_id'] for x in inventory)=={*(f'IN-{i:02}' for i in range(1,12)),*(f'EX-{i:02}' for i in range(1,10)),'SH-01'},'page IDs complete')
ids={f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}
chk(len(trace)==89 and {x['requirement_id'] for x in trace}==ids,'requirement trace exactly 89')
chk(all(x['verification_status']=='NOT_RUN' and x['page_refs'] and x['next_documents'] for x in trace),'trace fields/status')
chk(all(set(x['page_refs'].split(';')).issubset({y['page_id'] for y in inventory}) for x in trace),'trace invalid page refs')
chk(len(cases)==32 and {x['scenario_id'] for x in cases}=={f'UX-{i:02}' for i in range(1,33)},'32 cases numbered')
chk(all(x['execution_status']=='NOT_RUN' for x in cases),'scenarios actual status')
chk(all(x['page_id'] in {y['page_id'] for y in inventory} for x in cases),'scenario page refs')
for name in ('internal-engineering.html','cargopai-developers.html'):
 p=P/name;chk(p.is_file(),name+' missing')
 h=BeautifulSoup(p.read_text('utf8'),'html.parser')
 chk(bool(h.select('script')),name+' missing script')
 chk(bool(h.select('link[href="shared.css"]')),name+' missing shared tokens')
 chk('LOCAL MOCK' in h.get_text(' ',strip=True),name+' not clearly marked mock')
 # JS static syntax check against node
 code='\n'.join(x.get_text() for x in h.find_all('script') if not x.has_attr('src'))
 with tempfile.NamedTemporaryFile('w',suffix='.js',delete=True) as f:
  f.write(code);f.flush()
  result=subprocess.run(['node','--check',f.name],capture_output=True,text=True)
  chk(result.returncode==0,name+' JS syntax '+result.stderr[:150])
for path in [ROOT/'index.html',D/'F06_Developer_Experience.html',P/'internal-engineering.html',P/'cargopai-developers.html']:
 html=BeautifulSoup(path.read_text('utf8'),'html.parser')
 for tag in html.select('[href],[src]'):
  link=tag.get('href',tag.get('src',''))
  if not link or link.startswith(('http:','https:','mailto:','javascript:','#','data:')):continue
  filename=unquote(link.split('#',1)[0].split('?',1)[0]);chk((path.parent/filename).is_file(),f'{path.name} bad local link {link}')
chk((D/'F08_Delivery_and_Acceptance.md').is_file(),'F08 expected document missing')
chk('F06_Developer_Experience.html' in (ROOT/'index.html').read_text('utf8'),'index missing F06')
chk('F07' in (D/'README.md').read_text('utf8'),'F07 handoff absent')
if errors:raise SystemExit('FAIL F06:\n'+'\n'.join(errors))
print(f'PASS F06 static: chapters={len(chapters)} figures=8 pages={len(inventory)} trace={len(trace)} UX scenarios={len(cases)}; runtime/security tests NOT_RUN.')
