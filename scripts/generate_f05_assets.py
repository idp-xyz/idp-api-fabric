#!/usr/bin/env python3
"""Generate F05 DESIGN candidates. No runtime authentication or penetration tests are performed."""
from pathlib import Path
import csv,json,re
ROOT=Path(__file__).resolve().parents[1]
POL=ROOT/'governance/policies'; ACC=ROOT/'tests/acceptance'
POL.mkdir(parents=True,exist_ok=True);ACC.mkdir(parents=True,exist_ok=True)

def write_csv(path,cols,rows):
 with path.open('w',encoding='utf-8',newline='') as f:
  w=csv.DictWriter(f,fieldnames=cols); w.writeheader(); w.writerows(rows)
 return len(rows)

# principal, target, action, scope, enforcement PEP, fail behavior; allow-if never means unconditional allow
matrix_rows_raw=[
 ('internal_author','ContractRevision','revision.create','internal','workspace.member & product.same & action.role','control-plane','DENY'),
 ('internal_author','ContractRevision','revision.read','internal','workspace.member & product.same & action.role','control-plane','DENY'),
 ('internal_reviewer','ReviewDecision','release.approve','internal','review.assignment & independent.reviewer & digest.valid & policy.current','control-plane','DENY'),
 ('internal_publisher','PublicationSlot','publication.activate','internal','role.publisher & product.same & target.scope & decision.valid & block.absent & cas.match','control-plane','DENY'),
 ('internal_publisher','PublicationSlot','publication.withdraw','internal','role.publisher & product.same & slot.scope & cas.match','control-plane','DENY'),
 ('security_admin','AccessBlock','security.block','internal','role.security & target.scope & incident.logged','control-plane','DENY'),
 ('security_reviewer','AccessBlock','security.unblock','internal','role.security_review & recovery.approved & safety.rechecked','control-plane','DENY'),
 ('platform_admin','Product','product.register','internal','role.platform_admin & admin.scope','control-plane','DENY'),
 ('product_owner','Workspace','workspace.manage','internal','role.product_owner & product.same','control-plane','DENY'),
 ('product_owner','Member','member.grant','internal','role.product_owner & product.same & membership.valid','control-plane','DENY'),
 ('workspace_maintainer','APIAsset','asset.create','internal','workspace.member & product.same & role.maintainer','control-plane','DENY'),
 ('workspace_maintainer','APIAsset','asset.update','internal','workspace.member & product.same & role.maintainer','control-plane','DENY'),
 ('internal_reader','APIAsset','asset.search','internal','read.scope & product.same & row.filtered','catalog-PEP','DENY_OR_404'),
 ('internal_reader','APIAsset','asset.read','internal','read.scope & product.same & object.rechecked','control-plane','DENY_OR_404'),
 ('internal_reader','ContractRelease','contract.history','internal','release.scope & product.same & object.rechecked','registry-PEP','DENY_OR_404'),
 ('internal_reader','ContractRelease','contract.download','internal','release.scope & product.same & accessblock.absent','artifact-gateway','DENY_OR_404'),
 ('external_anonymous','PortalDocument','public.read','public','slot.public & release.published & block.absent & security.approved','portal-origin','DENY_OR_404'),
 ('external_developer','PortalDocument','partner.read','partner','developerorg.member & grant.active & product.same & audience.env.match & block.absent','portal-BFF','DENY_OR_404'),
 ('external_developer','APIAsset','partner.search','partner','developerorg.member & grant.active & product.same & row.filtered','catalog-PEP','DENY_OR_404'),
 ('external_developer','ContractRelease','partner.download','partner','developerorg.member & grant.active & release.visible & block.absent','artifact-gateway','DENY_OR_404'),
 ('external_developer','ConsumerApplication','application.read','partner','developerorg.member & application.owner_same & product.same','developer-access-PEP','DENY_OR_404'),
 ('external_org_admin','ConsumerApplication','application.register','partner','developerorg.admin & grant.policy & product.same','developer-access-PEP','DENY'),
 ('external_org_admin','AccessGrant','access.request','partner','developerorg.admin & product.entitled & audience.env.match','developer-access-PEP','DENY'),
 ('external_org_admin','Member','developer.member.manage','partner','developerorg.admin & org.same','developer-access-PEP','DENY'),
 ('explorer_user','ExplorerRequest','explorer.execute','sandbox','grant.active & product.same & endpoint.allowlisted & sandbox.mode & quota.ok','explorer-admission','DENY'),
 ('ci_service','ContractRevision','ci.revision.submit','service','token.audience & service.scope & source.allowed & product.same','control-plane','DENY'),
 ('ci_service','CheckRun','ci.check.read','service','token.audience & service.scope & product.same','governance-PEP','DENY'),
 ('worker','ImportJob','job.execute','service','workload.identity & lease.valid & job.scope & egress.restricted','worker-admission','DENY'),
 ('internal_auditor','AuditRecord','audit.search','internal','auditor.scope & product.allowed & fields.minimized','audit-PEP','DENY'),
 ('auditor_exporter','AuditRecord','audit.export','internal','role.audit_export & purpose.approved & scope.finite & stepup.valid','audit-export-PEP','DENY'),
 ('platform_operator','Connector','connector.status.read','internal','operator.scope & secrets.redacted','connector-PEP','DENY'),
 ('connector_admin','Connector','connector.rotate','internal','role.connector_admin & stepup.valid & purpose.approved','connector-PEP','DENY'),
 ('internal_reader','SearchIndex','search.raw_read','internal','never_allow_direct_index','search-gateway','DENY'),
 ('external_developer','ObjectStore','blob.direct_read','partner','never_allow_long_lived_unsigned_url','artifact-gateway','DENY'),
 ('product_owner','ReviewDecision','review.self_approve','internal','never_allow_self_approval','control-plane','DENY'),
]
m=[]
for i,(principal,res,action,channel,conditions,pep,fail) in enumerate(matrix_rows_raw,1):
 m.append(dict(rule_id=f'AC-{i:02d}',principal_role=principal,resource=res,action=action,channel=channel,allow_if=conditions,pep=pep,default_fail=fail,status='PROPOSED',runtime_verification='NOT_RUN'))
write_csv(POL/'F05_Access_Matrix.csv',list(m[0]),m)

# Threat registry: STRIDE, boundaries, attack, control IDs, assigned scenarios
threat_data=[
 ('S','P0','external IdP','Forged OIDC issuer, tokens with wrong audience','A01/A02','SEC-01;SEC-02'),
 ('S','P0','service identity','CI leaked access token reused as admin','A01/A09','SEC-03'),
 ('S','P0','webhook source','Unsigned webhook or replayed Git event','A06','SEC-17'),
 ('T','P0','Registry','Mutable Release blob replaced after approval','A03/A05','SEC-15'),
 ('T','P0','Policy Review','Reviewer decision reused after policy digest changes','A05/A11','SEC-13'),
 ('T','P0','Publication','Slot CAS races overwrite safe Release','A05','SEC-14'),
 ('R','P1','Audit','Approval cannot be tied to actor and immutable evidence','A08','SEC-23'),
 ('R','P1','Emergency operations','Break-glass actions not independently reviewed','A08/A11','SEC-24'),
 ('I','P0','Catalog Search','Cross-product asset metadata appears in search','A02/A04','SEC-04'),
 ('I','P0','Object Store','Unauthenticated direct blob download via guessed/presigned link','A02/A04','SEC-05'),
 ('I','P0','Portal CDN','Private document shared through public edge cache key','A04/A05','SEC-07'),
 ('I','P0','External portal','Revoked grant continues serving previously authorized content','A02/A05','SEC-06'),
 ('I','P0','Publication','Sensitive public Release persists after emergency deny','A05/A08','SEC-08'),
 ('I','P1','Logging','Authorization header / client secret leaks into logs','A07/A08','SEC-22'),
 ('I','P1','Explorer','Customer PII visible in shared Explorer history','A07','SEC-21'),
 ('D','P0','Contract Parser','Recursive YAML reference CPU or memory exhaustion','A06','SEC-11'),
 ('D','P0','Artifact import','ZIP bomb / oversized expanded file','A06','SEC-12'),
 ('D','P1','Control Plane','Abusive search/API requests exceed quota','A04/A06','SEC-25'),
 ('E','P0','Admin UI','Product admin escalates to platform admin','A02/A11','SEC-09'),
 ('E','P0','Reviewer','Author reviews own high-risk release','A11','SEC-10'),
 ('E','P0','Resolver','External $ref triggers SSRF metadata/internal network','A06','SEC-16'),
 ('E','P0','Explorer','User-supplied URL turns Explorer into open proxy','A06/A07','SEC-18'),
 ('E','P0','Worker','Trusted Worker uses broad credentials across products','A09','SEC-19'),
 ('E','P1','Database Pool','RLS transaction context leaks previous product','A02/A10','SEC-20'),
 ('T','P1','Recovery','Restore revives previously blocked publication','A05/A08','SEC-26'),
 ('I','P1','Audit Export','Operator exports all tenants without purpose and scope','A02/A08','SEC-27'),
 ('S','P1','Browser session','CSRF session fixation or stale session roles','A01/A02','SEC-28'),
 ('T','P1','CI supply chain','Compromised document renderer publishes unsafe HTML','A06/A09','SEC-29'),
 ('I','P1','Public publication','XSS executes from contract description','A06/A07','SEC-30'),
]
controls={
 'A01':'Trusted OIDC/OAuth and JWT validation with session protections',
 'A02':'Server-side scope and object-level authorization; deny-by-default',
 'A03':'Immutable Release Manifest/digest and controlled artifact integrity',
 'A04':'Authenticated artifact/search/portal origin with segregated caches',
 'A05':'CAS PublicationSlot plus monotonic AccessBlock and Edge reconciliation',
 'A06':'Hardened parser/ref resolver, sandboxing, resource/egress limits',
 'A07':'Explorer confinement, content sanitization, CSP and secret redaction',
 'A08':'Append-only audit, incident handling and recovery evidence',
 'A09':'Least-privileged workload identities, connector/secrets controls, SBOM',
 'A10':'DB transaction context/RLS defense in depth and pool isolation',
 'A11':'Separation of duties, step-up auth and binding approvals to digests',
}
t=[]
for i,(stride,sev,zone,attack,control,scens) in enumerate(threat_data,1):
 t.append(dict(threat_id=f'TH-{i:02d}',stride=stride,proposed_priority=sev,boundary=zone,attack_path=attack,controls=control,scenario_ids=scens,owner='TBD',residual_risk='REVIEW_REQUIRED',design_status='PROPOSED',runtime_verification='NOT_RUN'))
write_csv(POL/'F05_Threat_Register.csv',list(t[0]),t)

scenario_spec=[
 ('identity','Token with unknown issuer','deny and audit without leaking token','MVP'),
 ('identity','Valid signature, wrong token audience/ID token used as access token','deny type or audience mismatch','MVP'),
 ('service','Stolen CI token calls publication.activate','reject unsupported role/scope','MVP'),
 ('isolation','Parcel user searches private CargoPai assets','no IDs, names, counts or timing leakage within tolerances','MVP'),
 ('artifact','Cross-product blob direct URL is invoked','deny; no unauthorized release bytes','MVP'),
 ('revoke','Revoked grant repeatedly loads private release','origin denies; no stale private cache response','V1'),
 ('cache','Private BFF response entered shared CDN cache','private response cannot be publicly retrieved','MVP'),
 ('withdraw','AccessBlock created during public leak, CDN purge delayed','origin denies; edge status remains UNKNOWN/AT_RISK until proven','MVP'),
 ('roles','Product admin attempts global security action','deny via server-side action and scope','MVP'),
 ('roles','Contract author attempts to approve own high-risk revision','reject; independent review required','MVP'),
 ('input','Nested/recursive schema bombs parser','bounded timeout/memory; rejects without side effects','MVP'),
 ('input','Archive expands beyond extraction quota or uses ../ path','reject; no escape/no partial release','MVP'),
 ('review','Policy changes after signed decision, then activation attempted','require new review based on new policy digest','MVP'),
 ('publish','Concurrent CAS activation on same Target×APIAsset Slot','at most one winner; conflict reported','MVP'),
 ('integrity','Immutable Release object differs from signed/content manifest','block reading/publication, audit integrity incident','MVP'),
 ('ssrf','Remote $ref resolves or redirects to private/metadata IP','no request to forbidden IP; safe reject','MVP'),
 ('webhook','Valid webhook replay or invalid HMAC event','one effect or reject; no duplicate publication','V1'),
 ('explorer','Explorer tries arbitrary URL/DNS rebound/redirect','blocked by admission+egress, no internal request','V1'),
 ('worker','Worker attempts unauthorized product secret or egress','blocked with narrow workload identity','MVP'),
 ('database','Connection reuse retains prior product context under RLS','cannot leak rows across transactions','F07'),
 ('explorer','Explorer request/response contains secret and PII','not logged nor shared in history','V1'),
 ('logging','Sensitive headers/tokens in HTTP errors, traces, audit','redacted consistently across all sinks','MVP'),
 ('audit','Release published without linked actor and policy digest','transaction denied or evidence trail restored','MVP'),
 ('emergency','Break-glass release block undone by same operator without review','unblock refused; independent reapproval','MVP'),
 ('quota','Repeated search and large documents under abuse','quota/resource limits; no cross-tenant starvation','F07'),
 ('recovery','Restore older snapshot with revoked private Release','retain deny facts; no visibility resurrection','F07'),
 ('audit','Unauthorized audit bulk export requested','deny; no file created or reusable export link','V1'),
 ('session','Stale session after member revoke/CSRF attempt','deny sensitive action; CSRF token and cookie protections','MVP'),
 ('supply-chain','Compromised renderer dependency or unsafe update','detect via verification/CI gate; stop publishing','F07'),
 ('xss','Malicious Markdown and inline script embedded in API description','safe render; no script execution or secret access','MVP'),
 ('tenant','Different developer organizations share email domain','no implicit cross-org membership or grant','V1'),
 ('download','Expired signed blob URL still accepted by cache','reject/no-store or bound short expiry and risk documented','V1'),
 ('denial','Policy engine unavailable during write/high-risk read','deny and produce INDETERMINATE operational status','MVP'),
 ('recovery','Edge reports partial ACK after origin blocked','UNKNOWN/AT_RISK rather than success','MVP'),
 ('approval','Target audience changed after reviewer approval','invalid old approval, new scope review required','MVP'),
 ('multi-slot','Withdrawal of Shipment slot alters Tracking slot','other API slot remains unchanged unless target-wide block','MVP'),
]
s=[]
for i,(cat,scenario,expect,stage) in enumerate(scenario_spec,1):
 s.append(dict(scenario_id=f'SEC-{i:02d}',category=cat,scenario=scenario,expected_result=expect,stage=stage,execution_status='NOT_RUN',evidence_ref='',owner='TBD'))
write_csv(ACC/'F05_security_scenarios.csv',list(s[0]),s)

# Product requirement trace: exhaustive, with honest security responsibility.
related_direct=set('1 2 3 4 5 6 8 13 14 15 16 17 18 21 23 25 26 27 28 31 32 33 34 35 36 40 42 43 44 47 48 49 50 51 52 53 55 56 57 58 59 60 62 63 64 65 66 68'.split())
related_direct={int(x) for x in related_direct}
related_support=set('7 9 10 11 12 19 20 22 24 29 30 37 38 39 41 45 46 54 61 67'.split());related_support={int(x) for x in related_support}
NFR_direct={1,2,3,4,5,9,10,16,17,18};NFR_support={8,11,12,13,14,15}

def scope_reqs(code,num):
 if code=='FR':
  if num in related_direct:return 'DIRECT_SECURITY'
  if num in related_support:return 'SECURITY_SUPPORT'
  return 'NEXT_STAGE'
 if num in NFR_direct:return 'DIRECT_SECURITY'
 if num in NFR_support:return 'SECURITY_SUPPORT'
 return 'NEXT_STAGE'

def assign(req):
 number=int(req.split('-')[1]); isfr=req.startswith('FR-')
 if not isfr:
  if number in (1,2,16,18):return ('§5–11;§15','A01;A02;A04;A06','F07;F08')
  if number in (3,4,5,9):return ('§8–9;§13–16','A03;A05;A08','F07;F08')
  if number in (10,17):return ('§12–18','A08;A09','F07;F08')
  return ('§1;§18;§22','A02','F06;F07;F08')
 if number<=6:return ('§2;§5–7','A01;A02','F06;F07;F08')
 if number<=12:return ('§5–8','A02;A04','F07;F08')
 if number<=22:return ('§8;§10;§12','A03;A04;A06','F07;F08')
 if number<=30:return ('§6–10;§17','A05;A06;A11','F07;F08')
 if number<=39:return ('§7;§9;§16','A03;A05;A11','F07;F08')
 if number<=46:return ('§4–7;§14','A01;A02;A08','F06;F07;F08')
 if number<=55:return ('§4–6;§8;§11','A01;A02;A04;A07','F06;F07;F08')
 if number<=61:return ('§4;§12–13','A01;A09','F07;F08')
 if number<=68:return ('§14–18','A08;A09','F07;F08')
 return ('§1;§22','A02;A11','F07;F08;V2')
trace=[]
for prefix,start,end in [('FR',1,71),('NFR',1,18)]:
 for n in range(start,end+1):
  rid=f'{prefix}-{n:03d}' if prefix=='FR' else f'NFR-{n:02d}'
  sec, ctrl, nexts=assign(rid)
  trace.append(dict(requirement_id=rid,f05_coverage_class=scope_reqs(prefix,n),f05_sections=sec,control_refs=ctrl,next_documents=nexts,verification_status='NOT_RUN',evidence_note='Design-time mapping; not runtime evidence'))
write_csv(ACC/'F05_security_trace.csv',list(trace[0]),trace)

policy={
 '$schema':'https://json-schema.org/draft/2020-12/schema',
 'kind':'F05AuthorizationPolicyDesignExample','version':'V1.0','status':'PROPOSED',
 'validation':'STRUCTURE_ONLY_NOT_A_RUNTIME_PDP','decision_alg':'deny_overrides',
 'outcomes':['ALLOW','DENY','INDETERMINATE'],
 'default_outcome':'DENY',
 'inputs':{'principal':['issuer','subject','auth_time','token_audience','trusted_memberships'],
           'resource':['product_id','workspace_id','asset_id','release_id','target_id','slot_id','owner_id'],
           'context':['action','audience','environment','grant_generation','membership_generation','policy_digest','block_generation']},
 'controls':[{'id':k,'description':v,'status':'PROPOSED'} for k,v in controls.items()],
 'deny_rules':[
   {'id':'DENY-01','when':'not_authenticated_unless_explicit_public'},
   {'id':'DENY-02','when':'product_or_scope_mismatch'},
   {'id':'DENY-03','when':'missing_active_membership_or_grant'},
   {'id':'DENY-04','when':'effective_access_block_present'},
   {'id':'DENY-05','when':'release_or_approval_digest_invalid'},
   {'id':'DENY-06','when':'reviewer_is_author_for_high_risk'},
   {'id':'DENY-07','when':'policy_source_or_authorization_indeterminate'},
   {'id':'DENY-08','when':'operation_or_method_not_allowlisted'},
 ],
 'permit_rules':[{'rule_id':row['rule_id'],'principal_role':row['principal_role'], 'action':row['action'],'resource':row['resource'],'when_all':row['allow_if'].split(' & ')} for row in m if not row['allow_if'].startswith('never_allow')],
 'negative_rules':[row['rule_id'] for row in m if row['allow_if'].startswith('never_allow')],
 'notes':['No runtime policy engine is provided by this JSON design sample.','Server must load trusted relations and enforce object-level authorization on every read and write.','No real customer secret should be used in fixtures.']
}
(POL/'F05_Authorization_Policy_DRAFT.json').write_text(json.dumps(policy,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
print(f'F05 generated: matrix={len(m)} threats={len(t)} scenarios={len(s)} trace={len(trace)} policy_controls={len(controls)}')
