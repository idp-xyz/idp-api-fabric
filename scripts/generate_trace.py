#!/usr/bin/env python3
"""Regenerate design-stage FR/NFR trace links from the F01 source.
The mapping is a design hypothesis, NOT executed test evidence.
"""
from __future__ import annotations
import csv
import re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
MD=(ROOT/'docs/F01_Product_Requirements.md').read_text('utf-8')
OUT=ROOT/'tests/acceptance/F01_requirement_trace.csv'
# Each capability owns a stable broad value chain and primary design docs.
CAP={
 '01':('BG-01;BG-04','UC-01;UC-11','F02;F03;F05;F06'),
 '02':('BG-01;BG-04;BG-05','UC-01;UC-05','F02;F03;F06'),
 '03':('BG-02;BG-05','UC-02;UC-04','F02;F03;F04'),
 '04':('BG-02;BG-04','UC-03;UC-11','F04;F05;F07'),
 '05':('BG-02;BG-05','UC-04;UC-07;UC-08','F03;F04;F05'),
 '06':('BG-01;BG-02;BG-05','UC-02;UC-03;UC-04','F05;F06'),
 '07':('BG-03;BG-04','UC-05;UC-06;UC-07','F05;F06'),
 '08':('BG-01;BG-02;BG-05','UC-02;UC-04;UC-10','F02;F04;F07'),
 '09':('BG-02;BG-04;BG-05','UC-08;UC-11','F05;F07'),
 '10':('BG-06','UC-12','F04;F05;F07'),
}
# Only existing AT IDs. F08-TBD explicitly means no accepted specific scenario yet.
AT={
 '01':'AT-01;AT-04;AT-11','02':'AT-01;AT-04','03':'AT-01;AT-04','04':'AT-01;AT-04','05':'AT-11','06':'F08-TBD',
 '07':'AT-01;AT-11','08':'AT-04','09':'AT-05','10':'AT-01','11':'F08-TBD','12':'F08-TBD',
 '13':'AT-01','14':'AT-01','15':'AT-03','16':'AT-05','17':'AT-05;AT-08','18':'AT-05','19':'F08-TBD','20':'AT-05','21':'F08-TBD','22':'F08-TBD',
 '23':'AT-02;AT-12','24':'AT-02','25':'AT-03','26':'AT-02','27':'AT-02','28':'AT-02;AT-12','29':'F08-TBD','30':'F08-TBD',
 '31':'AT-05','32':'AT-03;AT-11','33':'AT-08','34':'AT-03','35':'AT-05;AT-09','36':'AT-09','37':'F08-TBD','38':'F08-TBD','39':'AT-06',
 '40':'AT-11','41':'AT-01;AT-11','42':'AT-02;AT-12','43':'AT-08;AT-09','44':'F08-TBD','45':'F08-TBD','46':'F08-TBD',
 '47':'AT-11','48':'AT-11','49':'AT-04;AT-11','50':'AT-11','51':'AT-07','52':'AT-07','53':'AT-07','54':'F08-TBD','55':'F08-TBD',
 '56':'AT-12','57':'AT-12','58':'F08-TBD','59':'AT-07','60':'AT-06','61':'F08-TBD',
 '62':'AT-03;AT-09','63':'AT-04;AT-11','64':'AT-08;AT-09','65':'AT-08;AT-10','66':'AT-10','67':'F08-TBD','68':'F08-TBD',
 '69':'F08-TBD','70':'F08-TBD','71':'F08-TBD',
}
AT={int(k):v for k,v in AT.items()}
NFR_AT={
 1:'AT-04',2:'AT-03',3:'AT-05',4:'AT-05',5:'AT-08;AT-09',6:'F08-TBD',7:'F08-TBD',8:'F08-TBD',9:'AT-10',
 10:'AT-08',11:'F08-TBD',12:'AT-02',13:'AT-01',14:'F08-TBD',15:'AT-11',16:'AT-04',17:'AT-12',18:'AT-03'
}
NFR_BG={
 1:'BG-04',2:'BG-02;BG-04',3:'BG-05',4:'BG-05',5:'BG-02;BG-05',6:'BG-01',7:'BG-01',8:'BG-01',9:'BG-02',
 10:'BG-02',11:'BG-01',12:'BG-02',13:'BG-01',14:'BG-03',15:'BG-03',16:'BG-04',17:'BG-02',18:'BG-02;BG-04'
}
NFR_DOC={1:'F05;F07',2:'F04;F05',3:'F03;F04',4:'F02;F04',5:'F02;F07',6:'F07;F08',7:'F07;F08',8:'F07;F08',9:'F07;F08',10:'F07',11:'F02;F07',12:'F04',13:'F02',14:'F06',15:'F06',16:'F05',17:'F07',18:'F05;F07'}
rows=[]
sections=list(re.finditer(r'^### 6\.(\d+) CAP-(\d+) ', MD, re.M))
for idx,m in enumerate(sections):
 cap=m.group(2)
 chunk=MD[m.end():sections[idx+1].start() if idx+1<len(sections) else MD.index('## 7.')]
 for z in re.finditer(r'^\| (FR-(\d{3})) \| ([^|]+) \| ([^|]+) \|$',chunk,re.M):
  rid,num,req,phase=z.groups(); bg,ucs,docs=CAP[cap]
  rows.append(dict(requirement_id=rid,type='FR',capability='CAP-'+cap,phase=phase.strip(),business_goals=bg,user_cases=ucs,design_documents=docs,acceptance_scenarios=AT[int(num)],trace_status='DESIGN_LINKS_ONLY',evidence_status='NOT_RUN',notes='详细领域规则/契约/UI/实际证据待 F02–F08 补齐'))
for n in range(1,19):
 rid=f'NFR-{n:02}'
 if not re.search(r'^\| '+re.escape(rid)+r' \|',MD,re.M): raise AssertionError(rid+' missing')
 rows.append(dict(requirement_id=rid,type='NFR',capability='Cross-cutting',phase='MVP/V1 依风险与发布门禁决定',business_goals=NFR_BG[n],user_cases='F08-TBD',design_documents=NFR_DOC[n],acceptance_scenarios=NFR_AT[n],trace_status='DESIGN_LINKS_ONLY',evidence_status='NOT_RUN',notes='目标与证据待责任人确认；不得认定已通过'))
if len(rows)!=89 or len({r['requirement_id'] for r in rows})!=89:raise AssertionError('Trace IDs not 89 unique')
OUT.parent.mkdir(parents=True,exist_ok=True)
with OUT.open('w',newline='',encoding='utf-8') as fp:
 w=csv.DictWriter(fp,fieldnames=list(rows[0]));w.writeheader();w.writerows(rows)
print('Generated',OUT.relative_to(ROOT),'rows',len(rows))
