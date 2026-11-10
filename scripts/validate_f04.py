#!/usr/bin/env python3
"""F04 offline illustrative lint subset + contract design asset validation.

Not a full OpenAPI/AsyncAPI spec validator. Never promote this script's PASS
into full compatibility, security or production publication approval.
Requires: PyYAML, beautifulsoup4 (optional for HTML checks), standard library.
"""
from __future__ import annotations
import csv
import hashlib
import json
import re
from pathlib import Path
from urllib.parse import urlsplit
import yaml

ROOT=Path(__file__).resolve().parents[1]
DOC=ROOT/'docs/F04_API_Contract_Governance.md'
OAS=ROOT/'contracts/examples/openapi/cargopai-contract-control.openapi.yaml'
ASYNC=ROOT/'contracts/examples/asyncapi/fabric-publication-events.asyncapi.yaml'
CAT=ROOT/'contracts/examples/catalog/cargopai-api-asset.catalog.json'
RULES=ROOT/'governance/policies/F04_Rules_Registry.json'
FIX=ROOT/'tests/acceptance/fixtures'
SCENARIOS=ROOT/'tests/acceptance/F04_governance_scenarios.csv'
TRACE=ROOT/'tests/acceptance/F04_contract_trace.csv'
METHODS={'get','post','put','patch','delete','head','options','trace'}

class UniqueKeyLoader(yaml.SafeLoader):
    pass

def unique_mapping(loader, node):
    mapping={}
    for key_node,val_node in node.value:
        k=loader.construct_object(key_node,deep=True)
        if not isinstance(k,(str,int,float,bool)):
            raise ValueError('ambiguous complex YAML mapping key')
        if k in mapping: raise ValueError(f'duplicate YAML mapping key: {k}')
        mapping[k]=loader.construct_object(val_node,deep=True)
    return mapping
UniqueKeyLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,unique_mapping)

def parse(text: str):
    if len(text.encode('utf-8'))>2_000_000: raise ValueError('illustrative lint input size limit exceeded')
    o=yaml.load(text,Loader=UniqueKeyLoader)
    if not isinstance(o,dict): raise ValueError('document root must be mapping')
    return o

def walk(v,ptr='$'):
    if isinstance(v,dict):
        for k,x in v.items():
            child=f'{ptr}/{k}'
            yield child,k,x
            yield from walk(x,child)
    elif isinstance(v,list):
        for i,x in enumerate(v): yield from walk(x,f'{ptr}/{i}')

def lint_oas(text: str):
    """Enforced examples only: checks are intentionally limited, documented."""
    findings=[]
    def add(rule,msg,loc='$'): findings.append({'rule_id':rule,'message':msg,'location':loc})
    try: data=parse(text)
    except (ValueError,yaml.YAMLError) as e:
        add('F04-GOV-009',str(e));return findings
    if data.get('openapi')!='3.1.2':add('F04-GOV-001','unsupported or missing illustrative OpenAPI baseline version')
    info=data.get('info')
    if not isinstance(info,dict) or not str(info.get('title','')).strip() or not str(info.get('version','')).strip():
        add('F04-GOV-002','required info.title/info.version missing')
    all_ops=[]
    paths=data.get('paths',{})
    if not isinstance(paths,dict):
        add('F04-GOV-009','paths should be mapping')
        paths={}
    global_security=data.get('security',None)
    for p,pobj in paths.items():
        if not isinstance(pobj,dict):continue
        for method,op in pobj.items():
            if method not in METHODS or not isinstance(op,dict):continue
            loc=f'paths/{p}/{method}'
            oid=op.get('operationId')
            if not isinstance(oid,str) or not oid.strip():add('F04-GOV-003','operationId missing',loc)
            else: all_ops.append((oid,loc))
            eff=op['security'] if 'security' in op else global_security
            if not isinstance(eff,list) or not eff:
                add('F04-GOV-006','effective security requirement missing',loc)
    seen={}
    for oid,loc in all_ops:
        if oid in seen:add('F04-GOV-008',f'duplicate operationId {oid} (first {seen[oid]})',loc)
        seen[oid]=loc
    # This subset is not a secrets scanner, and false-negative risk is explicitly accepted only for the demonstration.
    secret_patterns=[r'Bearer\s+(?!DESIGN_EXAMPLE)[A-Za-z0-9_-]{16,}',r'(?i)(?:api_key|client_secret|private_key)\s*[:=]\s*["\x27]?[A-Za-z0-9_/-]{12,}']
    for idx,line in enumerate(text.splitlines(),1):
        if any(re.search(rx,line) for rx in secret_patterns):
            add('F04-GOV-004','possible embedded credential (illustrative pattern only)',f'line:{idx}')
        if re.search(r'(?i)https?://(?:127\.0\.0\.1|localhost|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|169\.254\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.\d+\.\d+)(?=[:/\s"\x27]|$)',line):
            add('F04-GOV-010','internal IP/localhost URL disclosed',f'line:{idx}')
    for loc,k,v in walk(data):
        if k=='$ref' and isinstance(v,str):
            # This minimal fixture only accepts local JSON Pointers; full resolver belongs to F07.
            if not v.startswith('#/') or '..' in v or '\\' in v:
                add('F04-GOV-005','external or unsafe $ref (not supported by illustrative subset)',loc)
    return findings

def assert_negative():
    expected={
      'F04_negative_unsupported_version.yaml':'F04-GOV-001',
      'F04_negative_embedded_secret.yaml':'F04-GOV-004',
      'F04_negative_external_ref.yaml':'F04-GOV-005',
      'F04_negative_duplicate_operation_id.yaml':'F04-GOV-008',
      'F04_negative_missing_security.yaml':'F04-GOV-006',
      'F04_negative_duplicate_yaml_key.yaml':'F04-GOV-009',
    }
    for name,rule in expected.items():
        path=FIX/name
        found={x['rule_id'] for x in lint_oas(path.read_text(encoding='utf8'))}
        assert rule in found,f'{name} missing expected finding {rule}; found {found}'
    return len(expected)

def validate_assets():
    policy=json.loads(RULES.read_text(encoding='utf8'))
    ids=[r['rule_id'] for r in policy['rules']]
    assert len(ids)==len(set(ids)) and len(ids)>=25
    assert all(r['category'] in {'SECURITY_MANDATORY','INTEGRITY_MANDATORY','CONTRACT_COMPATIBILITY','QUALITY'} for r in policy['rules'])
    assert all(not r['waivable'] for r in policy['rules'] if r['category'] in {'SECURITY_MANDATORY','INTEGRITY_MANDATORY'})
    positive=lint_oas(OAS.read_text(encoding='utf8'))
    assert not positive, f'Positive sample lint issues: {positive}'
    asyncapi=parse(ASYNC.read_text(encoding='utf8'))
    assert asyncapi.get('asyncapi')=='3.1.0'
    assert isinstance(asyncapi.get('channels'),dict) and isinstance(asyncapi.get('operations'),dict)
    for op in asyncapi['operations'].values():
        assert op['action'] in ('receive','send')
    c=json.loads(CAT.read_text(encoding='utf8'))
    assert c['designation']=='DESIGN_EXAMPLE' and c['publication_authority']=='PublicationSlot'
    assert 'DESIGN_EXAMPLE' in DOC.read_text(encoding='utf8')
    with SCENARIOS.open(encoding='utf-8-sig',newline='') as f:
        scenarios=list(csv.DictReader(f))
    assert len(scenarios)==24 and all(x['verification_status']=='NOT_RUN' for x in scenarios)
    with TRACE.open(encoding='utf-8-sig',newline='') as f:
        trace=list(csv.DictReader(f))
    need={f'FR-{i:03d}' for i in range(1,72)} | {f'NFR-{i:02d}' for i in range(1,19)}
    assert {x['requirement_id'] for x in trace}==need and len(trace)==89
    assert all(x['verification_status']=='NOT_RUN' for x in trace)
    return len(ids),len(scenarios),len(trace)

if __name__=='__main__':
    a,b,c=validate_assets()
    n=assert_negative()
    print('F04 static subset: PASS; rules:',a,'; scenarios:',b,'; requirements:',c,'; negative fixtures:',n)
    print('NOTE: Official OAS/AsyncAPI schema, Go generator, IAM, Gateway, security and deployment tests: NOT_RUN')
