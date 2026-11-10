#!/usr/bin/env python3
"""Create F08 PLANNED delivery/acceptance assets. Never overwrite real evidence.
Status NOT_RUN is deliberate: this does not execute production or integration tests.
"""
from pathlib import Path
import csv, json, io
ROOT=Path(__file__).resolve().parents[1]
T=ROOT/'tests/acceptance'; T.mkdir(parents=True,exist_ok=True)
def csvdata(header,rows):
 b=io.StringIO(newline='');wr=csv.DictWriter(b,fieldnames=header,lineterminator='\n');wr.writeheader()
 for r in rows:wr.writerow({k:r.get(k,'') for k in header})
 return b.getvalue()
def safe_write(path,text):
 if path.exists():
  if path.read_text('utf8')!=text:raise RuntimeError(f'{path} exists with different contents; do not overwrite evidence. Review manually.')
  return
 path.write_text(text,'utf8')
def readrows(n):
 with (T/n).open(encoding='utf-8-sig',newline='') as f:return list(csv.DictReader(f))
wps=[
('Product/Governance','F01,F02','Pre-MVP','Project Manifest and blocking ADR','Product+Governance','STD-CP1'),
('Go CI and tools','WP-01','MVP','Go, CI, OAS parsing and build PoC','Engineering','STD-CP5'),
('Product access baseline','WP-02','MVP','Product/Workspace ACL, audit','Platform+Security','STD-CP2;STD-CP3'),
('Contract capture','WP-03','MVP','Source/ref digest, Revision, artifact store','Registry','STD-CP3'),
('Governance and review','WP-04','MVP','Lint/Diff/CheckRun/independent Review','Governance','STD-CP3'),
('Release and resilient publication','WP-05','MVP','Immutable Release, Slot CAS, Block, Outbox','Publication+Security','STD-CP3;STD-CP5'),
('Internal Engineering Portal','WP-03;WP-06','MVP','Live workflow and approval SoD','UX+Engineering','STD-CP4'),
('CargoPai Developer Portal','WP-06;WP-07','MVP','Authorized docs and Block visibility','Developer Experience+Security','STD-CP4'),
('MVP integration and acceptance','WP-02..WP-08','MVP','Real E2/E3 security/resilience and AT tests','QA+Security+Operations','STD-CP3;STD-CP5'),
('V1 Consumer onboarding','WP-09','V1','Real IAM/Gateway sandbox first call and grants','Integration+DeveloperAccess','STD-CP3;STD-CP4;STD-CP5'),
('V1 contract extension','WP-09','V1','AsyncAPI support, drift, announcements','Governance+DeveloperExperience','STD-CP3;STD-CP5'),
('Production and operations','WP-09;WP-10;WP-11 (as applicable)','PRODUCTION','DR, UAT, operations, approvals','Release+Operations','STD-CP5;STD-CP6')]
rows=[]
for i,(name,deps,phase,out,owner,gates) in enumerate(wps,1):rows.append(dict(work_package_id=f'WP-{i:02}',title=name,depends_on=deps,planned_phase=phase,minimum_output=out,accountable_role=owner,named_owner='TBD',estimated_person_days='TBD',p50_days='TBD',p90_days='TBD',required_gates=gates,delivery_status='NOT_IMPLEMENTED',implementation_evidence_uri=''))
safe_write(T/'F08_delivery_work_packages.csv',csvdata(['work_package_id','title','depends_on','planned_phase','minimum_output','accountable_role','named_owner','estimated_person_days','p50_days','p90_days','required_gates','delivery_status','implementation_evidence_uri'],rows))
ms=[('Adoption and decisions','WP-01','STD-CP1','Approved Manifest/owners/ADRs','Pre-MVP'),('Engineering foundation','WP-02','STD-CP5','PoC and reproducible build','MVP'),('Product and identity baseline','WP-03','STD-CP2;STD-CP3','Real negative ACL E2','MVP'),('Contract and policy','WP-04;WP-05','STD-CP3','Actual digest/revision/review tests','MVP'),('Publication and recovery','WP-06','STD-CP3;STD-CP5','CAS/Outbox/Block E3','MVP'),('Internal portal user journey','WP-07','STD-CP4','Authenticated review journeys','MVP'),('External docs MVP','WP-08;WP-09','STD-CP4;STD-CP5','CargoPai read+deny+rollback E3','MVP'),('V1 Developer onboarding','WP-10','STD-CP3;STD-CP4','Real Sandbox first call E3','V1'),('V1 extensions','WP-11','STD-CP3;STD-CP5','AsyncAPI + contracts verified','V1'),('Production handover','WP-12','STD-CP1;STD-CP2;STD-CP3;STD-CP4;STD-CP5;STD-CP6','Authorized UAT/DR/go-no-go E3/E4','PRODUCTION')]
rows=[]
for i,(name,wps_,gates,exit_,phase) in enumerate(ms):rows.append(dict(milestone_id=f'MS-{i:02}',name=name,work_packages=wps_,planned_phase=phase,exit_condition=exit_,required_gates=gates,accountable_role='TBD',planned_date='TBD',actual_date='',decision_status='NOT_RUN',approval_evidence_uri=''))
safe_write(T/'F08_milestones.csv',csvdata(['milestone_id','name','work_packages','planned_phase','exit_condition','required_gates','accountable_role','planned_date','actual_date','decision_status','approval_evidence_uri'],rows))
# 24 precise, execution-backed acceptance plans. Semicolon refs to tests already planned, not claims of execution.
ac=[
('MVP','Contract provenance','Snapshot source and reference-closure digests stable','FR-014;FR-016;NFR-03','EG-01;EG-02;EG-04','E2','Registry','STD-CP3'),
('MVP','Schema resource security','Reject external SSRF/cycles/oversized inputs','FR-016;NFR-18','EG-03','E3','Security','STD-CP3;STD-CP5'),
('MVP','OAS 3.1 support','Golden OAS parse+diff+Go output semantics supported subset','FR-020;FR-021;FR-023','EG-08;EG-09;EG-10','E2','Governance','STD-CP3'),
('MVP','Policy and waiver','Policy digest invalidates stale approval and waiver checked','FR-023;FR-024;FR-025','EG-06;EG-07','E2','Governance','STD-CP3'),
('MVP','Review SoD','No author self-approve and expired decision cannot publish','FR-026;FR-027','EG-11;EG-12','E3','Security','STD-CP3'),
('MVP','Immutable release','Release manifest and blob are tamper-evident and immutable','FR-031;FR-034;NFR-03','EG-05','E2','Registry','STD-CP3'),
('MVP','CAS concurrency','One successful Slot update; no stale silent overwrite','FR-033;NFR-05','EG-13;EG-14','E3','Publication','STD-CP3;STD-CP5'),
('MVP','Idempotency','Same key with different payload rejected without duplicate effect','FR-033','EG-15','E3','Publication','STD-CP3'),
('MVP','Outbox atomicity','CAS + audit + outbox one DB transaction; retry idempotent','FR-060;FR-061','EG-16;EG-17','E3','Operations','STD-CP3;STD-CP5'),
('MVP','Product isolation','Parcel cannot see CargoPai via search/detail/blob/audit','FR-001;FR-002;NFR-01;NFR-02','EG-26;EG-27;EG-28','E3','Security','STD-CP3;STD-CP5'),
('MVP','Emergency block','Server-side deny after Block; edge status UNKNOWN if unverifiable','FR-036;NFR-09','EG-19;EG-21','E3','Security+Operations','STD-CP3'),
('MVP','Release revert','Approved former Release restored by CAS with audit, without mutation','FR-036;NFR-03','EG-20','E3','Publication','STD-CP3'),
('MVP','Artifact failure compensation','DB/Object storage partial failure reconciles safely','FR-031;FR-060','EG-17;EG-18','E3','Operations','STD-CP3'),
('MVP','Worker fencing','Repeated and stale jobs do not duplicate or regress state','FR-060;FR-061','EG-22;EG-23;EG-24','E3','Operations','STD-CP5'),
('MVP','Internal workflow','Authenticated reviewer can finish review/publish and see errors','FR-039;FR-040;FR-041','EG-29','E2','UX+Engineering','STD-CP4'),
('MVP','External portal isolation','CargoPai docs read authorized, private versions denied','FR-048;FR-049;FR-050','EG-28;EG-30','E3','DeveloperExperience+Security','STD-CP4;STD-CP3'),
('MVP','Portal UX','Representative user tasks/keyboard/accessibility measured','NFR-15','EG-31','E2','UX','STD-CP4'),
('MVP','CI provenance','Reproducible build/tests, SBOM, dependency scan, immutable artifact refs','NFR-11;NFR-17','EG-34;EG-35','E2','Engineering','STD-CP5'),
('MVP','Contract drift gate','Gate rejects unapproved breaking changes','NFR-12;FR-023','EG-10','E2','Governance','STD-CP3'),
('V1','Developer application','ConsumerApp / Grant lifecycle integrated with real IAM','FR-052;FR-053','EG-32','E3','DeveloperAccess','STD-CP3;STD-CP5'),
('V1','Real sandbox first call','Authorized external developer invokes actual sandbox endpoint','FR-054;NFR-04','EG-33','E3','Integration','STD-CP4;STD-CP5'),
('V1','AsyncAPI compatibility','Producer/consumer/schema rendering and versioning checked','FR-022','EG-08','E2','Governance','STD-CP3'),
('PRODUCTION','Recovery & capacity','Approved SLO/RPO/RTO and real PITR/reconciliation drills','NFR-06;NFR-07;NFR-08;NFR-09','EG-37;EG-38;EG-39;EG-41','E3','Operations','STD-CP5;STD-CP6'),
('PRODUCTION','Production handover','UAT, security, release authority and post-deploy observations','NFR-10;NFR-16','EG-40;EG-42','E4','Release+Operations','STD-CP6')]
req={r['requirement_id'] for r in readrows('F01_requirement_trace.csv')}
eg={r['scenario_id'] for r in readrows('F07_engineering_scenarios.csv')}
rows=[]
for i,(stage,category,expected,reqs,tests,level,owner,gates) in enumerate(ac,1):
 # Some rows may refer to non-existent EG IDs? fail early.
 for rid in reqs.split(';'):assert rid in req,(i,rid)
 for tid in tests.split(';'):assert tid in eg,(i,tid)
 rows.append(dict(acceptance_id=f'AC-{i:02}',phase=stage,category=category,expected_observation=expected,requirement_refs=reqs,upstream_scenarios=tests,required_evidence_level=level,accountable_role=owner,named_owner='TBD',gate_refs=gates,execution_status='NOT_RUN',evidence_uri='',observed_result=''))
safe_write(T/'F08_acceptance_matrix.csv',csvdata(['acceptance_id','phase','category','expected_observation','requirement_refs','upstream_scenarios','required_evidence_level','accountable_role','named_owner','gate_refs','execution_status','evidence_uri','observed_result'],rows))
# Map each FR/NFR to a single planned delivery owner, at least one suitable acceptance family
base=readrows('F01_requirement_trace.csv'); eng=readrows('F07_engineering_trace.csv'); engby={r['requirement_id']:r for r in eng}
acbyreq={rid:[] for rid in req}
for r in rows:
 for rid in r['requirement_refs'].split(';'):acbyreq[rid].append(r['acceptance_id'])
capwp={'CAP-01':'WP-03','CAP-02':'WP-03','CAP-03':'WP-04','CAP-04':'WP-05','CAP-05':'WP-06','CAP-06':'WP-07','CAP-07':'WP-08','CAP-08':'WP-02','CAP-09':'WP-09','CAP-10':'DEFERRED_OUTSIDE_V1'}
phwork={'MVP':'WP-09','V1':'WP-10;WP-11','V1.0':'WP-10;WP-11','V1.x':'DEFERRED_OUTSIDE_V1','V2':'DEFERRED_OUTSIDE_V1','V2.0':'DEFERRED_OUTSIDE_V1','PRODUCTION':'WP-12'}
tr=[]
for b in base:
 rid=b['requirement_id'];e=engby[rid];p=b['phase'];phase='MVP/V1_CONDITIONAL' if 'MVP/V1' in p else 'V1.x' if 'V1.x' in p else 'V2' if 'V2' in p else 'V1' if 'V1' in p else 'MVP' if 'MVP' in p else p
 wp=capwp.get(b['capability'],'') if rid.startswith('FR-') else ''
 if not wp:wp=phwork.get(phase,'WP-09')
 if phase in ('V2','V1.x'):wp='DEFERRED_OUTSIDE_V1'
 if phase=='MVP/V1_CONDITIONAL':wp='WP-09;WP-12'  # Gate scope decides; no automatic MVP completion
 # CRITICAL: do not pretend each requirement has a matching real test: explicit deferred gap
 a=';'.join(acbyreq[rid]);link='PLANNED_AC' if a else 'BASELINE_AT_REQUIRES_DETAILED_CASE_BEFORE_STAGE_EXIT'
 tr.append(dict(requirement_id=rid,type=b['type'],capability=b['capability'],baseline_phase=p,planned_phase=phase,work_packages=wp,business_goals=b['business_goals'],f01_acceptance_scenarios=b['acceptance_scenarios'],f07_controls=e['engineering_controls'],f08_acceptance_ids=a,acceptance_link_status=link,delivery_status='NOT_IMPLEMENTED',verification_status='NOT_RUN',evidence_uri='',notes='F01 acceptance exists; add detailed executable case/evidence before stage gate' if not a else 'Planning link only, not passed'))
safe_write(T/'F08_delivery_trace.csv',csvdata(['requirement_id','type','capability','baseline_phase','planned_phase','work_packages','business_goals','f01_acceptance_scenarios','f07_controls','f08_acceptance_ids','acceptance_link_status','delivery_status','verification_status','evidence_uri','notes'],tr))
decisions=[
('Upper-standard CP4/CP5 cross-document wording','Governance','HIGH','F07 §18 summary must be harmonized with IDP-SCG-STD-001 formal gate naming','WP-01'),
('ADR-0001..0007 architecture and rollback scope','Architecture','HIGH','Approvals/impact tests required before binding implementation','WP-01'),
('Go/PostgreSQL/CI/toolchain exact versions','Engineering','HIGH','Run compatibility Golden Samples; record support matrix','WP-02'),
('IAM/Gateway/CDN interface & ownership','Integration+Security','HIGH','Actual adapter contract and isolated environment','WP-03;WP-10'),
('AccessBlock propagation and edge observability','Security+Operations','CRITICAL','Test source deny and visibility/status from multiple edges','WP-06'),
('SLO/Capacity/RPO/RTO and retention thresholds','Operations+Product','HIGH','Business load, failure impact, target approval and drills','WP-12'),
('Named owners, risk grades and standard profiles','Governance','HIGH','Approved Project Manifest and SoD matrix','WP-01'),
('Multi-product privacy, residency and legal agreements','Security+Legal','HIGH','Approved data inventory and controls','WP-01;WP-10'),
('Real V1 sandbox provider and first-call proof','DeveloperAccess','MEDIUM','Grant and real sandbox request+audit','WP-10'),
('Independent penetration, a11y and DR resourcing','QA+Security+Operations','HIGH','Specific environments/people and observable acceptance','WP-09;WP-12')]
rows=[]
for i,(issue,role,priority,closure,wps_) in enumerate(decisions,1):rows.append(dict(decision_id=f'OD-08-{i:02}',subject=issue,risk_priority_candidate=priority,accountable_role=role,named_owner='TBD',blocking_work_packages=wps_,required_decision_evidence=closure,status='OPEN',decision_uri='',approved_at=''))
safe_write(T/'F08_open_decisions.csv',csvdata(['decision_id','subject','risk_priority_candidate','accountable_role','named_owner','blocking_work_packages','required_decision_evidence','status','decision_uri','approved_at'],rows))
evidence={
 'schema_version':'1.0','template':'F08_EVIDENCE_RECORD','evidence_id':None,'scenario_id':'AC-01','requirement_refs':['FR-014'],'standard_id':'IDP-SCG-STD-001','gate_ids':['STD-CP3'],
 'source_commit':None,'artifact_digest':None,'config_digest':None,'policy_digest':None,'release_id':None,'publication_slot_id':None,'slot_generation':None,
 'environment':{'kind':None,'name':None,'location':None,'is_production':None,'synthetic_contract':None,'dependency_versions':{}},
 'tool_versions':{},'executor':None,'started_at':None,'finished_at':None,'expected':'Snapshot source and reference-closure digests stable','observed':None,
 'evidence_level_required':'E2','evidence_level_obtained':None,'result':'NOT_RUN','evidence_refs':[],'reviewer':None,'approver':None,'approval_at':None,
 'validity':{'scope_product':None,'scope_audience':None,'scope_environment':None,'expiry_at':None,'retest_triggers':['source_digest_change','policy_digest_change','deployment_change','permission_scope_change']},
 'notes':'DESIGN_TEMPLATE_ONLY; no real test has been performed; copy to a unique immutable record when run.'}
safe_write(T/'F08_evidence_record_TEMPLATE.json',json.dumps(evidence,ensure_ascii=False,indent=2)+'\n')
print('F08 planning assets generated:',len(wps),'WP',len(ms),'milestones',len(ac),'AC',len(tr),'requirements',len(decisions),'decisions',sum(not r['f08_acceptance_ids'] for r in tr),'requirements need detailed test mapping')
