#!/usr/bin/env python3
"""Idempotently update the existing F01-style index when F02 is available.
Preserves the original brand layout and all F01 links.
"""
from pathlib import Path
from bs4 import BeautifulSoup
ROOT=Path(__file__).resolve().parents[1]
p=ROOT/'index.html'
soup=BeautifulSoup(p.read_text('utf8'),'html.parser')
main=soup.find('main',class_='wrap')
if not main:raise RuntimeError('Unexpected index structure')
# Activate the existing F02 card (without touching the F01 content).
for card in soup.select('.grid section.card'):
 num=card.find(class_='num')
 if not num or num.get_text(strip=True)!='F02':continue
 h=card.find('h3');h.string='System Architecture'
 desc=card.find('p');desc.string='C4 系统上下文、Go DDD 模块化架构、契约权威、发布与撤销、安全隔离、双门户、存储一致性及运行恢复，八幅技术架构图。'
 status=card.find(class_='pending')
 if status:status.decompose()
 holder=h.parent
 if holder.find('a',href='docs/F02_System_Architecture.html') is None:
  link=soup.new_tag('a',href='docs/F02_System_Architecture.html');link.string='阅读 F02 HTML →';holder.append(link)
  split=soup.new_tag('span');split['style']='margin:0 8px;color:#b1c3cf';split.string='|';holder.append(split)
  md=soup.new_tag('a',href='docs/F02_System_Architecture.md');md.string='查看 Markdown';holder.append(md)
summary=soup.find(class_='summary')
if summary:
 boxes=summary.find_all('div',recursive=False)
 if len(boxes)>=2:
  boxes[1].clear()
  label=soup.new_tag('strong');label.string='F01 + F02';boxes[1].append(label)
  info=soup.new_tag('small');info.string='F01 内容定稿；F02 架构候选 / 待具名审核';boxes[1].append(info)
header=soup.find('header')
if header:
 for t in header.select('.strap span'):
  if t.get_text(strip=True)=='设计编制中':t.string='F01 / F02 已形成设计文档 · 待批准'
footer=soup.find(class_='footer')
if footer:footer.string='IDP API Fabric · V1.0 工程设计编制中 · F01 内容定稿待签收 / F02 架构候选待批准 · 尚未进入系统验收'
# Add F02-specific sources panel before dual-portal block.
if not soup.find(id='f02-assets'):
 target=next((h for h in soup.find_all('h2') if '双门户产品范围' in h.get_text()),None)
 h=soup.new_tag('h2',id='f02-assets');h.string='F02 架构决策与可执行追溯资产'
 block=BeautifulSoup('''<div class="panel"><div class="lanes"><div class="lane"><strong>六项架构决策（Proposed）</strong><p>保留 ADR-0001，并增加 ADR-0002～0006，涵盖 DDD 部署、来源权威、不可变发布、双门户与外部适配。</p><p><a href="decisions/ADR-0002.md">架构组织</a> · <a href="decisions/ADR-0003.md">来源权威</a> · <a href="decisions/ADR-0004.md">可靠发布</a> · <a href="decisions/ADR-0005.md">双门户</a> · <a href="decisions/ADR-0006.md">外部适配</a></p></div><div class="lane"><strong>需求追溯及架构场景</strong><p>将 71 项 FR、18 项 NFR 映射到 F02 设计章节；12 个架构风险场景全部保留 NOT_RUN。</p><p><a href="tests/acceptance/F02_architecture_trace.csv">架构需求追溯</a> · <a href="tests/acceptance/F02_architecture_scenarios.csv">验证场景</a></p></div></div></div>''','html.parser').div
 if target:
  target.insert_before(h);target.insert_before(block)
p.write_text(str(soup),encoding='utf8')
print('Updated index.html with F02 without changing edition')
