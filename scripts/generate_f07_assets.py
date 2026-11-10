#!/usr/bin/env python3
"""Generate F07 DESIGN-ONLY controls, verification plan and requirement trace.
No Go service, DB integration or real security tests are executed.
Do not overwrite manually accumulated runtime evidence once any status changes.
"""
from pathlib import Path
import csv,io,yaml
ROOT=Path(__file__).resolve().parents[1]
CONTROL=ROOT/'governance/policies/F07_Engineering_Controls_DRAFT.yaml'
SCENARIOS=ROOT/'tests/acceptance/F07_engineering_scenarios.csv'
TRACE=ROOT/'tests/acceptance/F07_engineering_trace.csv'
TOOLS=ROOT/'tests/acceptance/F07_tool_compatibility_matrix.csv'

def protected_write(path,content):
    if path.exists() and path.read_text('utf8')!=content:
        raise RuntimeError(f'Refusing to overwrite modified evidence/design file: {path}')
    path.write_text(content,encoding='utf8')

def emit_csv(cols,rows):
    io_=io.StringIO(newline='');w=csv.DictWriter(io_,fieldnames=cols,lineterminator='\n');w.writeheader();w.writerows(rows);return io_.getvalue()
controls=[
 ('Input provenance','受控源提交和快照必须绑定来源凭证、来源摘要、产品作用域','Contract','F04/F05','MVP','Parse+negative'),
 ('Ref security','拒绝非受控远程引用、目录逃逸、引用闭包逃逸与超额资源','Contract+Security','F04-GOV;F05','MVP','Sandbox+negative'),
 ('Revision integrity','Revision/Source/Ref digest 关系不可被静默替换','Registry','F03 INV-04','MVP','Property+DB'),
 ('Policy integrity','固定 Policy Digest 和 Tool Manifest 的检查证据','Governance','F04','MVP','Golden+negative'),
 ('Review binding','审批绑定输入、策略、受众及有效期并职责分离','Review','F04/F05','MVP','Integration+negative'),
 ('Release immutability','Release 制品写入后禁止覆盖，摘要重验','Registry','F03/F04','MVP','Blob+corrupt'),
 ('Slot CAS','PublicationSlot 唯一 Target×Asset；代数 CAS 更新','Publication','F03 INV-07','MVP','DB concurrent'),
 ('AccessBlock','紧急撤销权威状态在源站同步拒绝','Publication/Security','F05','MVP','Security+edge'),
 ('Authorization','全部敏感读写列表下载执行对象级访问判定','Security','F05 Matrix','MVP','HTTP/DB negative'),
 ('Workload identity','服务/Worker 用最小权限短时凭证，不保存 Secret 原文','Security','F05','MVP','Secrets+audit'),
 ('Outbox atomicity','权威写入与事件/审计同事务提交','Operations','F03/F02','MVP','DB failure'),
 ('Worker fencing','Lease fencing，重复投递必须幂等','Operations','F02','MVP','Crash/retry'),
 ('Reconciliation','Blob/DB/投影/Edge 能对账并显式 UNKNOWN','Operations','F02/F05','MVP','Fault injection'),
 ('Catalog boundaries','产品归属和目录搜索只能受控投影读取','Catalog','F03/F05','MVP','Cross-product'),
 ('API DTO isolation','传输生成 DTO 不穿透到领域实体','Engineering','F02 DDD','MVP','Dependency lint'),
 ('Dependency direction','领域层不得依赖 HTTP、SQL、云 SDK、其他 BC DB','Engineering','F02 DDD','MVP','Go dependency'),
 ('Tool capability','OAS/AsyncAPI 各特性标记明确支持范围','Governance','F04','MVP/V1','Golden matrix'),
 ('Compatibility policy','Breaking/Risky/Compatible/Unknown 结果有政策级门禁','Governance','F04','MVP','Diff tests'),
 ('DB migration','Expand/Backfill/Verify/Contract 且有故障回退路径','DB/Operations','F02/F03','MVP','Migration+restore'),
 ('Supply chain','依赖和 CI 工具可追溯，SBOM/签名/扫描','Security','F05 SSDF','MVP','SBOM+scan'),
 ('Build reproducibility','构建版本、工具、Digest 与不可重复因素有证据','Engineering','F04 Manifest','MVP','Rebuild proof'),
 ('CI gates','所有必须门禁失败/UNKNOWN 不得映射 PASS','Engineering','STD-CP1..6','MVP','Pipeline policy'),
 ('Audit integrity','高风险操作持久审计且查询受控','Operations/Security','F05','MVP','Audit tamper'),
 ('Observability','端到端 Trace / SLI 分层且日志不泄密','Operations','F02/F05','MVP','Telemetry audit'),
 ('Backups','数据库、Blob、审计备份恢复带一致性验证','Operations','F02','Before PROD','Recovery drill'),
 ('Internal portal','内部受控任务的错误/冲突/撤销/键盘状态完整','UX/Engineering','F06-A','MVP','Role journey'),
 ('External portal','外部公开与私有隔离、独立品牌/会话','UX/Security','F06-B','MVP','Browser+HTTP'),
 ('Developer onboarding','V1 ConsumerApp/Grant/Sandbox 必须真实 IAM/Gateway 证据','Integration','F06-B','V1','IAM/Gateway E2E'),
 ('Accessibility','关键任务键盘可用，V1 WCAG 2.2 AA 验证','UX','F06-C','MVP/V1','Keyboard+SR'),
 ('Evidence governance','真实执行必须携带环境、commit、工具版本、结果、证据','Delivery','F08','MVP/V1','Evidence schema')
]
controls_data={'schema_version':'1.0','document':'F07','status':'PROPOSED','actual_runtime_evidence':'NOT_RUN','controls':[{'control_id':f'ENG-{i:02}', 'category':cat,'statement':txt,'accountable_role':owner,'source_refs':ref,'planned_stage':stage,'verification_method':method,'verification_status':'NOT_RUN','approval':'TBD'} for i,(cat,txt,owner,ref,stage,method) in enumerate(controls,1)]}
protected_write(CONTROL,yaml.safe_dump(controls_data,allow_unicode=True,sort_keys=False,width=130))
scenario_specs=[
 ('REGISTRY','拒绝来源 Digest 不一致的受控导入','MVP','FR-014;FR-018','Registry','E2'),
 ('REGISTRY','外部 Ref 未批准时禁止联网解析','MVP','FR-016','Security','E2'),
 ('REGISTRY','Schema 循环/炸弹不得耗尽宿主资源','MVP','FR-016','Security','E3'),
 ('REGISTRY','Reference closure 内容变化后旧审批失效','MVP','FR-020;FR-025','Governance','E2'),
 ('REGISTRY','Release Blob 被覆盖/篡改需拒绝读取与发布','MVP','FR-031;FR-034','Registry','E2'),
 ('POLICY','政策 Digest 变化后旧 CheckRun 不可复用','MVP','FR-023;FR-025','Governance','E2'),
 ('POLICY','未经批准的 waiver 被拒绝','MVP','FR-024','Security','E2'),
 ('POLICY','无法解析的规范版本标为 UNSUPPORTED 不发布','MVP','FR-020','Governance','E2'),
 ('POLICY','Go DTO 的 OAS nullable 和 optional 语义一致','MVP','FR-021','Engineering','E2'),
 ('POLICY','Breaking change 被正确分类与阻断','MVP','FR-023','Governance','E2'),
 ('REVIEW','作者不能批准本人高风险变更','MVP','FR-026','Security','E3'),
 ('REVIEW','审批超时或撤销后旧 Decision 禁止发布','MVP','FR-027','Review','E2'),
 ('RELEASE','两个发布者争同一 Slot 的 CAS 只允许一人成功','MVP','FR-033','Publication','E3'),
 ('RELEASE','错误 expected_generation 不得自动覆盖','MVP','FR-033','Publication','E2'),
 ('RELEASE','同幂等键异负载拒绝且无重复生效','MVP','FR-033','Publication','E3'),
 ('RELEASE','DB 提交和 Outbox 审计强一致','MVP','FR-060;FR-061','Operations','E3'),
 ('RELEASE','对象已写而数据库失败后允许对账孤儿清理','MVP','FR-031;FR-060','Operations','E3'),
 ('RELEASE','数据库成功但 Artifact 不可读时禁止公布','MVP','FR-031;FR-032','Registry','E3'),
 ('REVOKE','AccessBlock 生效后私有文档所有读路径拒绝','MVP','FR-036;FR-052','Security','E3'),
 ('REVOKE','撤销和发布竞态不产生可见违规版本','MVP','FR-036','Publication','E3'),
 ('REVOKE','CDN 清理未知必须显示 UNKNOWN/AT_RISK','MVP','FR-036','Security','E3'),
 ('WORKER','Worker lease 过期后旧 fencing token 不得写入','MVP','FR-061','Operations','E3'),
 ('WORKER','重复事件消费不重复有效发布','MVP','FR-061','Operations','E3'),
 ('WORKER','Poison 任务有限重试并可人工隔离','MVP','FR-061','Operations','E3'),
 ('WORKER','投影落后时私有读取不能绕开主库授权','MVP','FR-052','Security','E3'),
 ('TENANCY','Parcel 主体不能访问 CargoPai 私有文档/搜索','MVP','FR-047;FR-052','Security','E3'),
 ('TENANCY','改产品时旧缓存/筛选/会话数据不得串产品','MVP','FR-040','Security/UX','E3'),
 ('TENANCY','直链 Blob 下载必须服务端重新授权','MVP','FR-052','Security','E3'),
 ('PORTAL','内部审核屏展示完整规则、摘要和拒绝原因','MVP','FR-042;FR-043','UX','E3'),
 ('PORTAL','外部门户公开/私有版本分离正确','MVP','FR-050;FR-052','UX/Security','E3'),
 ('PORTAL','手机端键盘导航、目录/横向表格可用','MVP','NFR-15','UX','E3'),
 ('INTEGRATION','V1 ConsumerApp/Grant 与 IAM Gateway 事实一致','V1','FR-053;FR-054','Integration','E3'),
 ('INTEGRATION','V1 首次沙箱调用具有真实 Gateway Trace','V1','FR-054','Integration','E3'),
 ('SUPPLY_CHAIN','CI 生成文件不可手改且可审计重建','MVP','NFR-16','Engineering','E2'),
 ('SUPPLY_CHAIN','关键依赖扫描/签名/SBOM 与发布摘要关联','MVP','NFR-12','Security','E2'),
 ('MIGRATION','Expand/Backfill/Verify/Contract 迁移具故障路径','MVP','NFR-14','Operations','E3'),
 ('RECOVERY','PITR 恢复后数据库与 Artifact 摘要一致','Before PROD','NFR-07;NFR-08','Operations','E3'),
 ('RECOVERY','恢复后 AccessBlock 继续阻断旧内容','Before PROD','FR-036','Security','E3'),
 ('CAPACITY','多产品热点 Slot/Outbox/搜索容量边界实测','Before PROD','NFR-03;NFR-04','Operations','E3'),
 ('TELEMETRY','指标/日志不包含令牌 Secret 和完整私有 Schema','MVP','NFR-10','Security','E3'),
 ('TELEMETRY','SLO 采集可区分公私门户与撤销指标','Before PROD','NFR-05','Operations','E3'),
 ('EVIDENCE','没有证据的测试不能转 PASS 且可追溯审核','MVP','NFR-18','Delivery','E2')
]
scenario_rows=[{'scenario_id':f'EG-{i:02}', 'category':cat,'scenario':title,'planned_stage':stage,'requirement_refs':refs,'accountable_role':owner,'required_evidence_level':level,'execution_status':'NOT_RUN','evidence_uri':'','actual_environment':'','notes':'Design-only scenario; to be executed F08 or implementation stage'} for i,(cat,title,stage,refs,owner,level) in enumerate(scenario_specs,1)]
protected_write(SCENARIOS,emit_csv(['scenario_id','category','scenario','planned_stage','requirement_refs','accountable_role','required_evidence_level','execution_status','evidence_uri','actual_environment','notes'],scenario_rows))

# Map groups explicitly to semantic design sections. This is design ownership, not evidence of implementation.
fr_groups=[(1,6,'§8,§9','ENG-09;ENG-14','MVP/V1'),(7,13,'§3,§9','ENG-14;ENG-16','MVP'),(14,19,'§4,§6','ENG-01;ENG-02;ENG-03;ENG-06','MVP'),(20,24,'§4,§10','ENG-04;ENG-17;ENG-18','MVP'),(25,28,'§3,§8','ENG-05;ENG-09','MVP'),(29,36,'§5,§6,§8','ENG-06;ENG-07;ENG-08;ENG-11','MVP'),(37,39,'§5,§13','ENG-21;ENG-24','MVP/V1'),(40,46,'§9,§11','ENG-26;ENG-29','MVP'),(47,55,'§8,§9','ENG-09;ENG-27;ENG-28','MVP/V1'),(56,61,'§7,§10','ENG-11;ENG-12;ENG-13','MVP'),(62,68,'§13,§15,§16','ENG-20;ENG-24;ENG-25','MVP/V1'),(69,71,'§1,§19,§20','ENG-30','V2')]
nfr_groups=[(1,2,'§8,§11','ENG-09;ENG-23'),(3,6,'§13,§14','ENG-24'),(7,9,'§14,§15','ENG-25'),(10,13,'§8,§16','ENG-09;ENG-20;ENG-24'),(14,15,'§9,§17','ENG-19;ENG-29'),(16,18,'§10,§11,§18','ENG-21;ENG-22;ENG-30')]
trace_rows=[]
for n in range(1,72):
  a,b,sections,ctrls,phase=next((a,b,sec,c,ph) for a,b,sec,c,ph in fr_groups if a<=n<=b)
  trace_rows.append({'requirement_id':f'FR-{n:03}','coverage_class':'ENGINEERING_RESPONSIBILITY' if n<=68 else 'RESERVED_FUTURE','f07_sections':sections,'engineering_controls':ctrls,'planned_stage':phase,'verification_status':'NOT_RUN','evidence_note':'Design mapping only; not executable implementation or test evidence'})
for n in range(1,19):
  a,b,sections,ctrls=next((a,b,sec,c) for a,b,sec,c in nfr_groups if a<=n<=b)
  trace_rows.append({'requirement_id':f'NFR-{n:02}','coverage_class':'QUALITY_RESPONSIBILITY','f07_sections':sections,'engineering_controls':ctrls,'planned_stage':'MVP/V1','verification_status':'NOT_RUN','evidence_note':'Acceptance SLO/owner TBD; design mapping only'})
protected_write(TRACE,emit_csv(['requirement_id','coverage_class','f07_sections','engineering_controls','planned_stage','verification_status','evidence_note'],trace_rows))

matrix_specs=[
 ('OAS31 parser','OpenAPI 3.1.2 parse and validate schema/ref closure','MVP'),
 ('OAS31 Go generator','required/null/union/oneOf/const, errors and headers','MVP'),
 ('OAS31 diff','Breaking/Risky/Compatible/Unknown and semantically risky changes','MVP'),
 ('OAS31 renderer','reference, sanitization, exposed private data, local links','MVP'),
 ('OAS32 parser','OpenAPI 3.2.1 roundtrip and feature preservation','V1 optional'),
 ('AsyncAPI31 parser','channels/operations/messages/schema and bindings','V1'),
 ('AsyncAPI31 renderer','operation direction and message examples','V1'),
 ('JSON Schema','2020-12 format and annotation/validation distinctions','MVP'),
 ('Go toolchain','Go/version, go fmt/vet/test/race/fuzz, generator reproducibility','MVP'),
 ('PostgreSQL','CAS, idempotency, audit/outbox transaction, restore','MVP'),
 ('Object Store','read-after-write, digest, immutability, failure/retention','MVP'),
 ('IAM Gateway','OIDC claims, client credentials, grant expiry, revoke','MVP/V1'),
 ('Portal Browser','desktop/tablet/mobile, keyboard, SR, session and ACL','MVP/V1'),
 ('CDN Edge','public cache invalidation/revocation propagation and receipts','Before PROD'),
 ('OpenTelemetry Go','trace/metrics/log signal support, PII sanitization','MVP')
]
matrix_rows=[{'capability_id':f'TC-{i:02}','tool_or_subsystem':name,'required_features':features,'planned_stage':stage,'candidate_version':'TBD','test_environment':'TBD','execution_status':'NOT_RUN','support_level':'UNVERIFIED','evidence_uri':'','notes':'Published product capability cannot be claimed until PoC and approval'} for i,(name,features,stage) in enumerate(matrix_specs,1)]
protected_write(TOOLS,emit_csv(['capability_id','tool_or_subsystem','required_features','planned_stage','candidate_version','test_environment','execution_status','support_level','evidence_uri','notes'],matrix_rows))
print(f'F07 generated: {len(controls)} controls, {len(scenario_rows)} scenarios, {len(trace_rows)} traces, {len(matrix_rows)} tool candidates. Runtime tests NOT_RUN.')
