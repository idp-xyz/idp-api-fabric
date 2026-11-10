#!/usr/bin/env python3
"""F05 documentation + design-matrix structural validator. NOT a security scanner, PDP or pentest."""
from pathlib import Path
from bs4 import BeautifulSoup
from urllib.parse import unquote
import csv,json,re
ROOT=Path(__file__).resolve().parents[1]
D=ROOT/'docs';G=ROOT/'governance/policies';T=ROOT/'tests/acceptance'
errors=[]
def check(ok,label):
 if not ok:errors.append(label)
def getcsv(f):
 with f.open(encoding='utf-8-sig',newline='') as inp:return list(csv.DictReader(inp))
source=(D/'F05_Security_and_Multitenancy.md').read_text('utf-8')
html=(D/'F05_Security_and_Multitenancy.html').read_text('utf-8')
soup=BeautifulSoup(html,'html.parser')
heads=re.findall(r'^##\s+(\d+)\.',source,re.M)
check(heads==[str(i) for i in range(24)],'Chapter numbering must be 0..23')
figs=re.findall(r'<figure\s+class="architecture-figure"',source)
check(len(figs)==10,'Expected exactly 10 figures in source')
check(len(soup.select('figure.architecture-figure'))==10,'Expected 10 rendered figures')
check(len(soup.find_all('h1'))==1,'Expected only one H1 in HTML')
check('F03 INV-08/09' in source and 'PublicationSlot' in source and 'AccessBlock' in source,'Publication security invariants missing')
check('ADR-0007' in source or (ROOT/'decisions/ADR-0007.md').is_file(),'Missing F05 ADR')
check('NOT_RUN' in source and 'Proposed' in source,'Missing candidate/nonexecution status')
check('F06' in (D/'README.md').read_text('utf8'),'F06 handoff missing')
ids=[x.get('id') for x in soup.select('[id]')]
check(len(ids)==len(set(ids)),'Duplicate HTML element IDs')
for a in soup.select('a[href^="#"]'):
 href=a.get('href','')
 if href!='#':check(href[1:] in ids,'Broken F05 section anchor: '+href)
# All local references from generated html and index resolve as offline files.
for f in [D/'F05_Security_and_Multitenancy.html',ROOT/'index.html']:
 h=BeautifulSoup(f.read_text('utf8'),'html.parser')
 for a in h.select('a[href]'):
  href=a['href']
  if not href or href.startswith(('https:','http:','mailto:','#','javascript:')):continue
  rel=unquote(href.split('#',1)[0].split('?',1)[0])
  if rel:check((f.parent/rel).is_file(),f'Broken local link in {f.name}: {href}')
check((D/'F05_Security_and_Multitenancy.html').is_file(),'HTML missing')
check((ROOT/'decisions/ADR-0007.md').is_file(),'ADR-0007 missing')
check('Proposed' in (ROOT/'decisions/ADR-0007.md').read_text('utf8'),'ADR-0007 not marked proposed')
policy=json.loads((G/'F05_Authorization_Policy_DRAFT.json').read_text('utf8'))
check(policy.get('status')=='PROPOSED','Draft policy status misrepresented')
check(policy.get('validation')=='STRUCTURE_ONLY_NOT_A_RUNTIME_PDP','Draft policy must state non-runtime')
check(policy.get('decision_alg')=='deny_overrides' and policy.get('default_outcome')=='DENY','Policy must be deny-first')
controls={x['id'] for x in policy.get('controls',[])}
check(controls=={f'A{i:02d}' for i in range(1,12)},'Missing controls A01..A11')
m=getcsv(G/'F05_Access_Matrix.csv')
check(len(m)==35 and {x['rule_id'] for x in m}=={f'AC-{i:02d}' for i in range(1,36)},'Access Matrix IDs/count invalid')
check(all(x['default_fail'] in ('DENY','DENY_OR_404') for x in m),'Access Matrix contains non-deny default')
check(all(x['runtime_verification']=='NOT_RUN' and x['status']=='PROPOSED' for x in m),'Access Matrix claims runtime validation')
check(all(x['pep'] and x['resource'] and x['action'] and x['allow_if'] for x in m),'Access Matrix incomplete row')
check(set(policy.get('negative_rules',[]))=={r['rule_id'] for r in m if r['allow_if'].startswith('never_allow')},'Policy negative rules mismatch')
check({x['rule_id'] for x in policy.get('permit_rules',[])}=={r['rule_id'] for r in m if not r['allow_if'].startswith('never_allow')},'Policy allow candidates mismatch')
th=getcsv(G/'F05_Threat_Register.csv')
check(len(th)==29 and {x['threat_id'] for x in th}=={f'TH-{i:02d}' for i in range(1,30)},'Threat IDs/count invalid')
check({x['stride'] for x in th}==set('STRIDE'),'Threat registry lacks one or more STRIDE categories')
check(all(x['runtime_verification']=='NOT_RUN' and x['residual_risk']=='REVIEW_REQUIRED' for x in th),'Threat registry claims execution or risk acceptance')
check(all(x['owner']=='TBD' and x['design_status']=='PROPOSED' for x in th),'Threat owner/status ambiguous')
sc=getcsv(T/'F05_security_scenarios.csv')
check(len(sc)==36 and {x['scenario_id'] for x in sc}=={f'SEC-{i:02d}' for i in range(1,37)},'Scenario IDs/count invalid')
check(all(x['execution_status']=='NOT_RUN' and not x['evidence_ref'] for x in sc),'Scenario falsely claimed as executed')
scids={x['scenario_id'] for x in sc}
for x in th:
 check(all(c.strip() in controls for c in x['controls'].split('/')),f"Threat {x['threat_id']} references unknown control")
 check(all(c.strip() in scids for c in x['scenario_ids'].split(';')),f"Threat {x['threat_id']} references unknown scenario")
trace=getcsv(T/'F05_security_trace.csv')
expected={f'FR-{i:03d}' for i in range(1,72)}|{f'NFR-{i:02d}' for i in range(1,19)}
check(len(trace)==89 and {x['requirement_id'] for x in trace}==expected,'F05 trace lacks exact 89 F01 requirements')
check(all(x['verification_status']=='NOT_RUN' for x in trace),'Trace claims verification')
check(all(x['f05_coverage_class'] in ('DIRECT_SECURITY','SECURITY_SUPPORT','NEXT_STAGE') and x['f05_sections'] and x['control_refs'] for x in trace),'Trace mapping incomplete')
# Verify completed F08 phase exists.
for n,name in [(1,'F01_Product_Requirements'),(2,'F02_System_Architecture'),(3,'F03_Domain_and_Data_Model'),(4,'F04_API_Contract_Governance')]:
 check((D/(name+'.md')).is_file(),f'F0{n} upstream source missing')
for name in ('F08_Delivery_and_Acceptance',):
 check((D/(name+'.md')).is_file(),f'F08 expected document missing')
if errors:
 raise SystemExit('FAIL F05 static consistency:\n'+'\n'.join(sorted(set(errors))))
print(f'PASS F05 design static: chapters={len(heads)} figures={len(figs)} access_rules={len(m)} threats={len(th)} scenarios={len(sc)} trace={len(trace)}; no runtime or security tests executed.')
