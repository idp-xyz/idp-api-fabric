#!/usr/bin/env python3
"""Validate F08 design artifacts; real implementation tests remain NOT_RUN."""
from pathlib import Path
from bs4 import BeautifulSoup
from urllib.parse import unquote
import csv,json,re
ROOT=Path(__file__).resolve().parents[1];D=ROOT/'docs';T=ROOT/'tests/acceptance'
errors=[]
def ck(x,m):
 if not x:errors.append(m)
def rr(name):
 with (T/name).open(encoding='utf-8-sig',newline='') as f:return list(csv.DictReader(f))
md=(D/'F08_Delivery_and_Acceptance.md').read_text('utf8');hp=D/'F08_Delivery_and_Acceptance.html';s=BeautifulSoup(hp.read_text('utf8'),'html.parser')
ck(re.findall(r'^##\s+(\d+)\.\s',md,re.M)==[str(i) for i in range(22)],'F08 chapters must be 0-21')
ck(md.count('<figure class="architecture-figure"')==9 and len(s.select('figure.architecture-figure'))==9,'F08 must have nine meaningful figures')
ck(len(s.find_all('h1'))==1,'F08 HTML needs exactly one H1')
ck('Proposed' in md or 'PROPOSED' in md,'Proposed state missing')
ck('NOT_RUN' in md and 'TBD' in md and 'STD-CP1' in md and 'STD-CP6' in md,'F08 statuses/gates missing')
for n in range(1,8):ck((D/f'F{n:02}_'+ 'unused').exists() if False else len(list(D.glob(f'F{n:02}_*.md')))==1,f'Unexpected F{n:02} files')
for n in range(1,9):
 ck(len(list(D.glob(f'F{n:02}_*.md')))==1 and len(list(D.glob(f'F{n:02}_*.html')))==1,f'F{n:02} Markdown/HTML pair missing')
w=rr('F08_delivery_work_packages.csv');m=rr('F08_milestones.csv');ac=rr('F08_acceptance_matrix.csv');dc=rr('F08_detailed_acceptance_cases.csv');tr=rr('F08_delivery_trace.csv');op=rr('F08_open_decisions.csv')
ck(len(w)==12 and {x['work_package_id'] for x in w}=={f'WP-{i:02}' for i in range(1,13)},'Expected 12 unique WPs')
ck(len(m)==10 and {x['milestone_id'] for x in m}=={f'MS-{i:02}' for i in range(10)},'Expected 10 unique milestones')
ck(len(ac)==24 and {x['acceptance_id'] for x in ac}=={f'AC-{i:02}' for i in range(1,25)},'Expected 24 unique acceptance cases')
ck(len(dc)==51 and {x['acceptance_id'] for x in dc}=={f'DC-{i:03}' for i in range(1,52)},'Expected 51 detailed acceptance cases')
ck(len(op)==10 and {x['decision_id'] for x in op}=={f'OD-08-{i:02}' for i in range(1,11)},'Expected 10 unique decisions')
expected={f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}
ck(len(tr)==89 and {x['requirement_id'] for x in tr}==expected,'F08 89 FR/NFR trace broken')
ck(all(x['delivery_status']=='NOT_IMPLEMENTED' and x['verification_status']=='NOT_RUN' and not x['evidence_uri'] for x in tr),'F08 trace falsifies actual verification')
ck(all(x['execution_status']=='NOT_RUN' and not x['evidence_uri'] and x['named_owner']=='TBD' for x in ac),'F08 AC contains false results')
ck(all(x['execution_status']=='NOT_RUN' and not x['evidence_uri'] and not x['observed_result'] and x['named_owner']=='TBD' for x in dc),'F08 DC contains false actual test results')
ck(all(x['fixture_preconditions'] and x['test_steps'] and x['expected_observation'] and x['negative_or_boundary_assertion'] and x['authoritative_observation'] and x['required_evidence_level'] in ('E2','E3','E4') for x in dc),'F08 detailed cases lack reproducible design or evidence requirement')
ck(all(x['delivery_status']=='NOT_IMPLEMENTED' and not x['implementation_evidence_uri'] for x in w),'WBS claims implementation')
ck(all(x['decision_status']=='NOT_RUN' and not x['approval_evidence_uri'] for x in m),'Milestones falsely approved')
ck(all(x['status']=='OPEN' and x['named_owner']=='TBD' and not x['decision_uri'] for x in op),'F08 decisions falsely approved')
ck(all(x['work_packages']=='DEFERRED_OUTSIDE_V1' or all(y in {z['work_package_id'] for z in w} for y in x['work_packages'].split(';')) for x in tr),'Trace refers to unknown work packages')
acids={x['acceptance_id'] for x in ac}|{x['acceptance_id'] for x in dc}
ck(all(set(x['f08_acceptance_ids'].split(';'))-set([''])<=acids for x in tr),'Trace references unknown AC/DC IDs')
ck(all(bool(x['f08_acceptance_ids']) for x in tr),'F08 direct acceptance coverage incomplete')
ck({x['requirement_refs'] for x in dc}=={x['requirement_id'] for x in tr if 'PLANNED_DETAILED_CASE'==x['acceptance_link_status']},'Detailed-case requirement trace mismatch')
ck(all(x['phase_gate'] in {'MVP_STAGE_EXIT','V1_STAGE_EXIT_ONLY','DEFERRED_TO_PHASE_EXIT','SCOPE_APPROVAL_REQUIRED_BEFORE_GATE'} for x in dc),'Invalid detailed-case stage gate')
scids={x['scenario_id'] for x in rr('F07_engineering_scenarios.csv')}
for x in ac:
 ck(set(x['requirement_refs'].split(';'))<=expected,f"Unknown requirement in {x['acceptance_id']}")
 ck(set(x['upstream_scenarios'].split(';'))<=scids,f"Unknown EG scenario in {x['acceptance_id']}")
 ck(all(y in {'STD-CP'+str(i) for i in range(1,7)} for y in x['gate_refs'].split(';')),f"Unknown gate in {x['acceptance_id']}")
for x in tr:
 ck(bool(x['f01_acceptance_scenarios']),f"Missing baseline AT for {x['requirement_id']}")
 ck(x['acceptance_link_status'] in ('PLANNED_AC','PLANNED_DETAILED_CASE'),f"Bad acceptance status {x['requirement_id']}")
template=json.loads((T/'F08_evidence_record_TEMPLATE.json').read_text('utf8'))
ck(template['result']=='NOT_RUN' and template['started_at'] is None and template['observed'] is None and template['approver'] is None,'Evidence template claims real execution')
ids=[x.get('id') for x in s.select('[id]')];ck(len(ids)==len(set(ids)),'F08 duplicate HTML IDs')
for a in s.select('a[href^="#"]'):
 url=a.get('href');ck(url=='#' or url[1:] in ids,'F08 broken anchor '+url)
for p in [ROOT/'index.html',hp]:
 soup=BeautifulSoup(p.read_text('utf8'),'html.parser')
 for a in soup.select('[href],[src]'):
  link=a.get('href',a.get('src',''))
  if not link or link.startswith(('https:','http:','mailto:','javascript:','#','data:')):continue
  link=unquote(link.split('#')[0].split('?')[0]);ck((p.parent/link).is_file(),f'{p.name}: local link missing {link}')
ck('F08_Delivery_and_Acceptance.html' in (ROOT/'index.html').read_text('utf8'),'F08 not linked in unified index')
if errors:raise SystemExit('FAIL F08:\n'+'\n'.join(sorted(set(errors))))
print(f'PASS F08 design/static: chapters=22 figures=9 WP={len(w)} MS={len(m)} core_AC={len(ac)} detailed_DC={len(dc)} directly_linked={len(tr)} decisions={len(op)}; runtime tests NOT_RUN.')
