#!/usr/bin/env python3
"""Generate F02-specific requirement-to-architecture map (design evidence only)."""
from pathlib import Path
import csv,re
ROOT=Path(__file__).resolve().parents[1]
F01=(ROOT/'docs/F01_Product_Requirements.md').read_text(encoding='utf8')
TRACE=ROOT/'tests/acceptance/F02_architecture_trace.csv'
CASES=ROOT/'tests/acceptance/F02_architecture_scenarios.csv'
# Strongest architecture responsibility by F01 capability group
specs=[
 (range(1,7),'§3,§6','ADR-0001,ADR-0005','F03,F05,F06'),
 (range(7,13),'§5,§7,§13','ADR-0001,ADR-0004','F03,F04'),
 (range(13,23),'§7,§8,§13','ADR-0003,ADR-0004','F03,F04,F07'),
 (range(23,31),'§8,§9','ADR-0004','F04,F05,F07'),
 (range(31,40),'§7,§9,§13','ADR-0004','F03,F04,F07'),
 (range(40,47),'§4,§10','ADR-0005','F05,F06'),
 (range(47,56),'§6,§10,§11','ADR-0005','F05,F06'),
 (range(56,62),'§8,§12','ADR-0006','F04,F07'),
 (range(62,69),'§11,§14','ADR-0002,ADR-0004','F05,F07,F08'),
 (range(69,72),'§16','ADR-0006','F04,F05,F08'),
]
nfr={
 1:('§6,§11','ADR-0005','F05'),2:('§6,§11','ADR-0005','F05'),3:('§7,§13','ADR-0004','F03,F04'),4:('§7,§9','ADR-0004','F04'),
 5:('§9,§13','ADR-0004','F07'),6:('§2,§14','ADR-0002','F07'),7:('§2,§15','ADR-0002','F07'),8:('§14','ADR-0002','F07'),
 9:('§9,§14','ADR-0004','F07,F08'),10:('§14','ADR-0002','F07'),11:('§5,§15','ADR-0002','F07'),12:('§7,§8','ADR-0004','F04'),
 13:('§4,§15','ADR-0001','F03,F07'),14:('§10','ADR-0005','F06'),15:('§10','ADR-0005','F06'),16:('§6,§11','ADR-0005','F05'),
 17:('§11,§15','ADR-0006','F07'),18:('§7,§11','ADR-0003','F05,F07')}
rows=[]
for i in range(1,72):
 key=f'FR-{i:03d}'
 if key not in F01:raise RuntimeError(f'Missing source {key}')
 s=next(((sec,adrs,next_) for vals,sec,adrs,next_ in specs if i in vals),None)
 if s is None:raise RuntimeError(key)
 rows.append([key,*s,'NOT_RUN','F02 design allocation only; no implementation/test evidence'])
for i in range(1,19):
 key=f'NFR-{i:02d}'
 if key not in F01:raise RuntimeError(f'Missing source {key}')
 rows.append([key,*nfr[i],'NOT_RUN','Metrics/quality verification pending F07/F08'])
with TRACE.open('w',newline='',encoding='utf-8-sig') as f:
 w=csv.writer(f);w.writerow(['requirement_id','f02_sections','architecture_decisions','subsequent_documents','verification_status','evidence_note']);w.writerows(rows)
cases=[]
for line in (ROOT/'docs/F02_System_Architecture.md').read_text(encoding='utf8').splitlines():
 m=re.match(r'^\| (AR-\d+) \| (.*?) \| (.*?) \| (.*?) \| (NOT_RUN) \|',line)
 if m:cases.append(list(m.groups()))
assert len(cases)==12,len(cases)
with CASES.open('w',newline='',encoding='utf-8-sig') as f:
 w=csv.writer(f);w.writerow(['scenario_id','scenario','f01_requirements','expected_observable_result','execution_status']);w.writerows(cases)
print('F02 trace:',len(rows),'requirements,',len(cases),'architecture scenarios; no tests executed')
if __name__=='__main__':pass
