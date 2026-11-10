#!/usr/bin/env python3
"""Independent cross-artifact V1.0 design consistency guard, NOT runtime certification.
Refuses fabricated approvals, out-of-sync Slot pointers, CP4/CP5 drift, uncovered FR/NFR.
"""
from pathlib import Path
import csv,json,re,sys
from collections import Counter
R=Path(__file__).resolve().parents[1]
D=R/'docs';T=R/'tests/acceptance';P=R/'governance/policies'
errors=[]
def assert_(condition,message):
 if not condition:errors.append(message)
def read_csv(path):
 with path.open(encoding='utf-8-sig',newline='') as f:return list(csv.DictReader(f))
f02=(D/'F02_System_Architecture.md').read_text('utf8')
f03=(D/'F03_Domain_and_Data_Model.md').read_text('utf8')
f07=(D/'F07_Engineering_and_Quality.md').read_text('utf8')
f08=(D/'F08_Delivery_and_Acceptance.md').read_text('utf8')
adr=(R/'decisions/ADR-0004.md').read_text('utf8')
for name,txt in [('F02',f02),('F03',f03),('F07',f07),('F08',f08),('ADR-0004',adr)]:
 assert_('PublicationSlot' in txt,f'{name} lacks publication slot model')
for name,txt in [('F02',f02),('F03',f03),('ADR-0004',adr)]:
 assert_('UNIQUE(target_id, api_asset_id)' in txt or 'UNIQUE(target_id,api_asset_id)' in txt,f'{name} lacks unique slot key')
for name,txt in [('F02',f02),('ADR-0004',adr)]:
 assert_('expected_generation' in txt and 'AccessBlock' in txt,f'{name} lacks slot CAS or target-wide deny distinction')
assert_('Publication Target 当前代数' not in f02,'F02 retains incorrect Target CAS')
assert_('Publication Target 以数据库条件更新记录当前有效 Release' not in adr,'ADR-0004 retains incorrect Target pointer')
for name,txt in [('F07',f07),('F08',f08)]:
 assert_(bool(re.search(r'STD-CP4[^\n|]*专业体验',txt)),f'{name} CP4 not defined as professional UX')
 assert_(bool(re.search(r'STD-CP5[^\n|]*工程与质量',txt)),f'{name} CP5 not defined as engineering quality')
assert_('F05 多租户授权、威胁模型、负向测试计划 | 安全 Owner TBD' not in f07,'F07 still maps CP4 to security')
tr=read_csv(T/'F08_delivery_trace.csv')
ac=read_csv(T/'F08_acceptance_matrix.csv');dc=read_csv(T/'F08_detailed_acceptance_cases.csv')
expect={f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}
assert_(len(tr)==89 and {r['requirement_id'] for r in tr}==expect,'Missing/extra requirement in trace')
ids={r['acceptance_id'] for r in ac+dc}
assert_(len(ids)==len(ac)+len(dc)==75,'Acceptance IDs must be 24 core AC + 51 detailed DC, unique')
assert_(all(r['f08_acceptance_ids'] and set(r['f08_acceptance_ids'].split(';'))<=ids for r in tr),'Every requirement needs direct F08 AC/DC')
assert_(all(r['execution_status']=='NOT_RUN' and not r['evidence_uri'] for r in ac+dc),'Design AC/DC fabricated execution')
assert_(all(r['delivery_status']=='NOT_IMPLEMENTED' and r['verification_status']=='NOT_RUN' for r in tr),'Design trace falsely claims delivery')
assert_(all(r['named_owner']=='TBD' for r in ac+dc),'Unsubstantiated named owner in design acceptance')
assert_(all(x['required_evidence_level'] in ('E2','E3','E4') and x['test_steps'] and x['negative_or_boundary_assertion'] and x['phase_gate'] for x in dc),'Detailed cases missing steps/negative/gates')
assert_(len([x for x in dc if x['phase_gate']=='MVP_STAGE_EXIT'])>=24,'Not all MVP gap cases appear as MVP exit requirements')
planned_by={x['requirement_id']:x['planned_phase'] for x in tr}
for row in ac:
 if row['phase']=='MVP':
  assert_(all(planned_by[req] not in ('V1','V1.x','V2') for req in row['requirement_refs'].split(';')),f"MVP AC incorrectly requires later feature: {row['acceptance_id']}")
assert_(all((x['phase_gate']!='MVP_STAGE_EXIT') or (x['phase']=='MVP') for x in dc),'Detailed cases with wrongly-scoped MVP gate')

manifest=json.loads((P/'F01_Project_Manifest_DRAFT.json').read_text('utf8'))
assert_(manifest['adoption']['status']=='DRAFT_NOT_APPROVED' and manifest['risk_level'] is None,'Manifest approval falsely marked')
assert_(manifest.get('freeze_condition')=='BLOCKED_PENDING_NAMED_ADOPTION_AND_RELEVANT_ADR_APPROVALS','No explicit freeze blocking condition')
assert_(all(x.get('status') in ('OPEN','NOT_RUN') for x in manifest['approval_prerequisites'].values()),'False approval prerequisite status')
for f in sorted((R/'decisions').glob('ADR-*.md')):
 assert_('Proposed' in f.read_text('utf8'),f'{f.name} falsely approved')
assert_(all(r['status']=='OPEN' for r in read_csv(T/'F08_open_decisions.csv')),'Open decisions incorrectly closed')
for p in [D/'F08_Delivery_and_Acceptance.md',R/'README.md',D/'README.md']:
 assert_('F08_detailed_acceptance_cases.csv' in p.read_text('utf8'),f'New detail cases undiscoverable from {p.name}')
if errors:
 print('FAIL CONSISTENCY V1.0:',*errors,sep='\n- ')
 sys.exit(1)
print(f'PASS consistency-design-only: 7 ADR Proposed, Manifest DRAFT, core_AC={len(ac)}, detail_DC={len(dc)}, direct_requirements={len(tr)}, all runtime NOT_RUN.')
