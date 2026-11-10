#!/usr/bin/env python3
"""Generate F03 machine-readable design indexes from the F03 Markdown.
Does not execute a database, approve an ADR, or mark any business test PASS.
"""
from __future__ import annotations
import csv
import re
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
text=(ROOT/'docs/F03_Domain_and_Data_Model.md').read_text(encoding='utf-8')

def extract_table_between(start:str,end:str):
    a=text.split(start,1)[1].split(end,1)[0]
    rows=[]
    for line in a.splitlines():
        if not line.startswith('|'):continue
        cols=[x.strip() for x in line.strip().strip('|').split('|')]
        if all(re.fullmatch(r':?-+:?',c) for c in cols):continue
        rows.append(cols)
    return rows

def output(file:str,header:list[str],body:list[list[str]]):
    path=ROOT/file
    path.parent.mkdir(parents=True,exist_ok=True)
    with path.open('w',encoding='utf-8-sig',newline='') as f:
        w=csv.writer(f);w.writerow(header);w.writerows(body)
    print('Generated',file,'rows=',len(body))

# Logical model table is explicitly owned by F03 Markdown; CSV is a regenerated index.
model=extract_table_between('### 5.4 数据字典的伴随机器资产','## 6. 数据权威、制品摘要与可追溯发布链')
fields=['BC','Aggregate','Entity','PrimaryKey','RequiredReferences','Uniqueness','Cardinality','DataRetention','Phase']
assert model[0]==fields,(model[0],fields)
assert len(model[1:])>=35 and all(len(row)==len(fields) for row in model[1:])
output('docs/F03_Logical_Model.csv',fields,model[1:])

# Maps are intentionally *design allocation*, not test evidence or artificial "89 database entities".
allocation=[
 (1,6,'ProductGovernance','Product;Workspace;WorkspaceMembership;INV-01;INV-02','§2;§3;§4;§7','F05;F07','DOMAIN_OWNER'),
 (7,12,'APICatalog','APIAsset;AssetOwnership;INV-03','§2;§3;§4;§5','F04;F06','DOMAIN_OWNER'),
 (13,22,'ContractRegistry','SourceSnapshot;ContractRevision;ContractRelease;INV-04;INV-05','§4;§5;§6;§8','F04;F07','DOMAIN_OWNER'),
 (23,30,'PolicyReview','PolicySnapshot;CheckRun;ReviewDecision;INV-06;INV-07','§4;§6;§8;§9','F04;F05','DOMAIN_OWNER'),
 (31,39,'Publication','PublicationTarget;PublicationSlot;AccessBlock;INV-08;INV-09','§4;§5;§8;§10;§11','F04;F05;F07','DOMAIN_OWNER'),
 (40,46,'CrossContextReadModels','WorkspaceNavigationView;AuthorizedCatalogView;PublicationTimelineView','§3;§7;§15','F05;F06','DOMAIN_SUPPORT'),
 (47,50,'PublicationAndAccess','AuthorizedDocumentView;PublicationSlot;AccessGrant','§7;§10;§15','F05;F06','DOMAIN_SUPPORT'),
 (51,55,'DeveloperAccess','DeveloperOrganization;ConsumerApplication;AccessGrant;INV-10;INV-11','§2;§4;§7;§15','F05;F06','DOMAIN_OWNER'),
 (56,61,'PlatformOperations','Job;OutboxEvent;InboxDedup;INV-12;INV-13','§9;§12','F04;F07','DOMAIN_SUPPORT'),
 (62,68,'PlatformOperations','AuditRecord;Job;AccessBlock;Reconciliation;INV-09;INV-12;INV-13','§4;§11;§12;§13','F05;F07;F08','DOMAIN_OWNER'),
 (69,71,'FutureAgentGovernance','ReleaseRef;PolicyRef;CapabilityRef','§1;§16;§17','F04;F05;F08','FUTURE_SCOPE'),
]
reqs=set(re.findall(r'\bFR-\d{3}\b', (ROOT/'docs/F01_Product_Requirements.md').read_text(encoding='utf-8')))
reqs|=set(re.findall(r'\bNFR-\d{2}\b', (ROOT/'docs/F01_Product_Requirements.md').read_text(encoding='utf-8')))
assert {f'FR-{i:03}' for i in range(1,72)}|{f'NFR-{i:02}' for i in range(1,19)}==reqs,('F01 requirements not exact',len(reqs))
rows=[]
for i in range(1,72):
    block=next(a for a in allocation if a[0]<=i<=a[1])
    _,_,bc,refs,sections,next_docs,scope=block
    rows.append([f'FR-{i:03}',scope,bc,refs,sections,next_docs,'NOT_RUN','Design mapping only; not business test evidence'])
nfr=[
 ('SecurityScope','Product;Membership;AccessGrant;INV-02;INV-11','§7;§11','F05;F08'),
 ('SecurityScope','AccessBlock;INV-09','§7;§11','F05;F08'),
 ('ArtifactIntegrity','Release;ArtifactManifest;INV-05','§5;§6','F04;F07'),
 ('Traceability','SourceSnapshot;Revision;Decision;Release;Slot','§5;§6;§14','F04;F07'),
 ('Consistency','Slot;DeliveryState;OutboxEvent;INV-08;INV-13','§8;§10;§12','F07;F08'),
 ('PerformanceBudget','AuthorizedCatalogView;PublicationTimelineView','§3;§15','F07;F08'),
 ('CapacityBudget','Release;Revision;Job;Projection','§5;§13','F07;F08'),
 ('AvailabilityBudget','Slot;AccessBlock;Recovery','§11;§13','F07;F08'),
 ('Recovery','AuditRecord;Reconciliation;ArtifactManifest;INV-09','§11;§13','F07;F08'),
 ('Observability','Job;Outbox;Audit;DeliveryState','§12;§15','F07'),
 ('Maintainability','SevenContexts;PublishedLanguage','§3;§17','F07'),
 ('Compatibility','Revision;Release;PolicyDecision','§6;§8','F04'),
 ('NewProductOnboarding','Product;Workspace;Target','§1;§2;§7','F06;F07'),
 ('I18N','PortalDocumentView;locale metadata pending','§15','F06'),
 ('Accessibility','PortalTaskState;UX validation pending','§15','F06;F08'),
 ('PrivacyRetention','Audit;ReviewDecision;OAuthClientRef','§13','F05;F07'),
 ('SupplyChainIntegrity','ArtifactManifest;BuildEvidence','§6;§13','F07'),
 ('UntrustedContent','SourceSnapshot;RefClosureManifest','§6;§11','F05;F07'),
]
assert len(nfr)==18
for i,(bc,refs,sec,downstream) in enumerate(nfr,1):
    rows.append([f'NFR-{i:02}','DOMAIN_SUPPORT',bc,refs,sec,downstream,'NOT_RUN','Design allocation; SLO and security gates require real evidence'])
assert len(rows)==89 and len(set(x[0] for x in rows))==89
output('tests/acceptance/F03_domain_trace.csv',['requirement_id','coverage_class','responsible_context_or_concern','model_refs','f03_sections','next_documents','verification_status','evidence_note'],rows)

scenarios=extract_table_between('### 14.2 F03 设计验证场景（Definition of Verifiability）','### 14.3 与上位六道门禁的关系')
assert len(scenarios)==19 and scenarios[0][:2]==['场景 ID','可检验的业务结果']
expected=[f'DM-{i:02}' for i in range(1,19)]
assert [r[0] for r in scenarios[1:]]==expected
assert all(row[-1]=='NOT_RUN' for row in scenarios[1:])
output('tests/acceptance/F03_domain_scenarios.csv',['scenario_id','expected_observable_result','upstream_references','execution_status'],scenarios[1:])
