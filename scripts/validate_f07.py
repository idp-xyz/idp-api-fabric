#!/usr/bin/env python3
"""F07 design-only static validation. DOES NOT execute Go, DB, IAM, security or resilience tests."""
from pathlib import Path
from bs4 import BeautifulSoup
from urllib.parse import unquote
import csv, hashlib, re, yaml
ROOT=Path(__file__).resolve().parents[1];D=ROOT/'docs';T=ROOT/'tests/acceptance';G=ROOT/'governance/policies'
errors=[]
def chk(pred,reason):
 if not pred:errors.append(reason)
def read_rows(p):
 with p.open('r',encoding='utf-8-sig',newline='') as f:return list(csv.DictReader(f))
source=(D/'F07_Engineering_and_Quality.md').read_text('utf8');page=(D/'F07_Engineering_and_Quality.html').read_text('utf8');soup=BeautifulSoup(page,'html.parser')
chapters=re.findall(r'^##\s+(\d+)\.\s',source,re.M)
chk(chapters==[str(i) for i in range(22)],'F07 section numbers should be 0-21')
chk(source.count('<figure class="architecture-figure"')==9,'F07 Markdown figure count is 9')
chk(len(soup.select('figure.architecture-figure'))==9,'F07 HTML figure count is 9')
chk(len(soup.find_all('h1'))==1,'F07 must contain one HTML H1')
chk('NOT_RUN' in source and 'Proposed' in source and 'F08' in source,'F07 status or F08 handoff absent')
chk(all(x in source for x in ('F06-A','F06-B','F06-C','PublicationSlot','AccessBlock','PostgreSQL','Outbox','Go','STD-CP1','STD-CP6')),'required engineering concepts absent')
anchors=[x.get('id') for x in soup.select('[id]')]
chk(len(anchors)==len(set(anchors)),'duplicate id in F07 HTML')
for a in soup.select('a[href^="#"]'):
 href=a.get('href');chk(href=='#' or href[1:] in anchors,'broken HTML anchor '+href)
policy=yaml.safe_load((G/'F07_Engineering_Controls_DRAFT.yaml').read_text('utf8'))
controls=policy['controls'];control_ids={c['control_id'] for c in controls}
chk(len(controls)==30 and control_ids=={f'ENG-{i:02}' for i in range(1,31)},'30 unique control IDs')
chk(policy.get('status')=='PROPOSED' and policy.get('actual_runtime_evidence')=='NOT_RUN','policy falsely approved')
chk(all(c['verification_status']=='NOT_RUN' and c['approval']=='TBD' and c['source_refs'] for c in controls),'controls claim executed')
trace=read_rows(T/'F07_engineering_trace.csv')
required={f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}
chk(len(trace)==89 and {c['requirement_id'] for c in trace}==required,'89 requirement trace rows')
chk(all(c['verification_status']=='NOT_RUN' and c['f07_sections'] and c['engineering_controls'] for c in trace),'trace missing refs or false test PASS')
for c in trace:
 chk(all(x in control_ids for x in c['engineering_controls'].split(';')),f"unknown control for {c['requirement_id']}")
scenario=read_rows(T/'F07_engineering_scenarios.csv');scids={x['scenario_id'] for x in scenario}
chk(len(scenario)==42 and scids=={f'EG-{i:02}' for i in range(1,43)},'42 engineering verification scenarios')
chk(all(x['execution_status']=='NOT_RUN' and not x['evidence_uri'] and x['accountable_role'] for x in scenario),'scenario evidence falsely claimed')
for x in scenario:
 chk(all(y in required for y in x['requirement_refs'].split(';')),f"Scenario {x['scenario_id']} unknown FR/NFR")
matrix=read_rows(T/'F07_tool_compatibility_matrix.csv');chk(len(matrix)==15 and {x['capability_id'] for x in matrix}=={f'TC-{i:02}' for i in range(1,16)},'15 tool compatibility rows')
chk(all(x['execution_status']=='NOT_RUN' and x['support_level']=='UNVERIFIED' and not x['evidence_uri'] for x in matrix),'Tool matrix claims actual validation')
chk('F07_Engineering_and_Quality.html' in (ROOT/'index.html').read_text('utf8'),'index F07 link missing')
chk('F08' in (D/'README.md').read_text('utf8'),'F08 handoff absent')
chk((D/'F08_Delivery_and_Acceptance.md').is_file(),'F08 expected document missing')
for pagepath in [ROOT/'index.html',D/'F07_Engineering_and_Quality.html']:
 psoup=BeautifulSoup(pagepath.read_text('utf8'),'html.parser')
 for tag in psoup.select('[href],[src]'):
  link=tag.get('href',tag.get('src',''))
  if not link or link.startswith(('http:','https:','mailto:','javascript:','#','data:')):continue
  link=unquote(link.split('#')[0].split('?')[0]);chk((pagepath.parent/link).is_file(),f'Broken relative link {pagepath.name}: {link}')
if errors:raise SystemExit('FAIL F07:\n'+'\n'.join(sorted(set(errors))))
print(f'PASS F07 static: chapters={len(chapters)} figures=9 controls={len(controls)} scenarios={len(scenario)} tools={len(matrix)} trace={len(trace)}; business/system/security/DR tests NOT_RUN.')
