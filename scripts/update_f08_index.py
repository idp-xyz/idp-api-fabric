#!/usr/bin/env python3
"""Activate F08 in the existing index, documentation handoff and older HTML navigation."""
from pathlib import Path
from bs4 import BeautifulSoup
ROOT=Path(__file__).resolve().parents[1]
path=ROOT/'index.html'
s=BeautifulSoup(path.read_text('utf8'),'html.parser')
card=next((x for x in s.select('section.card') if x.find(class_='num') and x.find(class_='num').get_text(strip=True)=='F08'),None)
assert card is not None,'F08 card missing'
card.clear()
num=s.new_tag('div',attrs={'class':'num'});num.string='F08';card.append(num)
text=s.new_tag('div');h=s.new_tag('h3');h.string='Delivery & Acceptance';text.append(h)
p=s.new_tag('p');p.string='12 个实施工作包、10 个里程碑、24 个关键验收、六道上位标准门禁、证据与运营交接；9 幅图。';text.append(p)
for href,label in [('docs/F08_Delivery_and_Acceptance.html','阅读 F08 HTML →'),('docs/F08_Delivery_and_Acceptance.md','查看 Markdown')]:
 a=s.new_tag('a',href=href);a.string=label;text.append(a)
 if 'html' in href:
  sp=s.new_tag('span');sp['style']='margin:0 8px;color:#b1c3cf';sp.string='|';text.append(sp)
card.append(text)
summary=s.find(class_='summary')
if summary:
 boxes=summary.find_all('div',recursive=False)
 if len(boxes)>=2:
  boxes[1].clear();st=s.new_tag('strong');st.string='F01–F08';boxes[1].append(st)
  sub=s.new_tag('small');sub.string='八份文档已形成候选设计；真实系统与安全验收 NOT_RUN，仍待批准';boxes[1].append(sub)
for node in s.select('.strap span'):
 if '已形成' in node.get_text(' ',strip=True):node.string='F01–F08 已形成设计文档 · 待具名批准'
footer=s.find(class_='footer')
if footer:footer.string='IDP API Fabric · V1.0 工程设计基线 · F01–F08 完整候选文档 · 生产验收 NOT_RUN'
if s.find(id='f08-assets') is None:
 target=next((h for h in s.find_all('h2') if '双门户产品范围' in h.get_text()),None)
 assert target is not None
 heading=s.new_tag('h2',id='f08-assets');heading.string='F08 · 实施路线图与真实验收资产'
 block=BeautifulSoup('''<div class="panel"><div class="lanes"><div class="lane"><strong>实施交付与里程碑</strong><p>12 个 WBS 工作包、10 个阶段里程碑；依赖、角色与风险待具名批准，不虚设工期。</p><p><a href="docs/F08_Delivery_and_Acceptance.html">阅读 F08</a> · <a href="tests/acceptance/F08_delivery_work_packages.csv">WBS</a> · <a href="tests/acceptance/F08_milestones.csv">里程碑</a></p></div><div class="lane"><strong>正式验收与证据</strong><p>24 项关键验收、89 条 FR/NFR 追溯、10 项未决；真实运行证据保持 NOT_RUN。</p><p><a href="tests/acceptance/F08_acceptance_matrix.csv">验收矩阵</a> · <a href="tests/acceptance/F08_delivery_trace.csv">需求追溯</a> · <a href="tests/acceptance/F08_evidence_record_TEMPLATE.json">证据模板</a></p></div></div></div>''','html.parser').div
 target.insert_before(heading);target.insert_before(block)
path.write_text(str(s),'utf8')
# Historical handoff in docs/README remains for audit; update top status and add an explicit completion note.
idx=ROOT/'docs/README.md';txt=idx.read_text('utf8')
txt=txt.replace('| F08 | Delivery & Acceptance | 规划中，尚未创建 |','| F08 | [Delivery & Acceptance](F08_Delivery_and_Acceptance.md) · [HTML](F08_Delivery_and_Acceptance.html) | **V1.0 交付与验收设计候选，待批准、真实验收 NOT_RUN** |')
txt=txt.replace('F07 已完成设计候选；F08 仍为未创建的规划文档。','F07/F08 均已形成设计候选，真实系统实施与验收尚未开始。')
anchor='## 当前可检查资产'
notice='''## F08 交付与验收设计（当前有效）

- [F08 Markdown 权威源](F08_Delivery_and_Acceptance.md) / [离线 HTML](F08_Delivery_and_Acceptance.html)：12 工作包、10 里程碑、24 核心验收、六道标准门禁与九幅示意图。
- [F08 交付工作包](../tests/acceptance/F08_delivery_work_packages.csv) / [里程碑](../tests/acceptance/F08_milestones.csv) / [验收矩阵](../tests/acceptance/F08_acceptance_matrix.csv) / [89 项需求交付追溯](../tests/acceptance/F08_delivery_trace.csv)。
- [10 项待决](../tests/acceptance/F08_open_decisions.csv) / [真实证据模板](../tests/acceptance/F08_evidence_record_TEMPLATE.json)。**所有真实执行测试仍为 NOT_RUN；无人签署生产 GO。**
- F01–F07 Markdown 权威正文和 ADR-0001–0007 保持不变。原交接说明保留在本索引下方，供追溯。F08 指出 F07 门禁概括映射与上位标准的局部差异，待具名裁决。

'''
if '## F08 交付与验收设计（当前有效）' not in txt:txt=txt.replace(anchor,notice+anchor)
idx.write_text(txt,'utf8')
# Existing build scripts must keep F08 link after a rebuild, without touching prior Markdown documents.
for script in ['build_f02.py','build_f03.py','build_f04.py','build_f05.py','build_f06.py','build_f07.py']:
 p=ROOT/'scripts'/script;t=p.read_text('utf8');t2=t.replace("('F08','Delivery & Acceptance',None)","('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')")
 if t!=t2:p.write_text(t2,'utf8')
# Patch already rendered older HTML navigation so visitors can open the newly completed F08 directly.
for n in range(2,8):
 matches=list((ROOT/'docs').glob(f'F{n:02}_*.html'))
 if len(matches)!=1:continue
 p=matches[0];s2=BeautifulSoup(p.read_text('utf8'),'html.parser')
 for item in s2.select('.navlist .pending'):
  if 'F08' in item.get_text(' ',strip=True):
   a=s2.new_tag('a',href='F08_Delivery_and_Acceptance.html');a.string='F08 · Delivery & Acceptance';item.replace_with(a)
 p.write_text(str(s2),'utf8')
print('Updated index, README and cross-document navigation through F08')
