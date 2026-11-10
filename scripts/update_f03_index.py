#!/usr/bin/env python3
"""Idempotent index update for F03; retains original F01/F02 cards and visual system."""
from pathlib import Path
from bs4 import BeautifulSoup
ROOT=Path(__file__).resolve().parents[1]
p=ROOT/'index.html';soup=BeautifulSoup(p.read_text(encoding='utf-8'),'html.parser')
assert soup.find('main',class_='wrap') is not None,'Unexpected index layout'
for c in soup.select('.grid section.card'):
 num=c.find(class_='num')
 if not num or num.get_text(strip=True)!='F03':continue
 h=c.find('h3');assert h
 h.string='Domain & Data Model'
 desc=c.find('p');assert desc
 desc.string='七个候选 DDD 限界上下文、37 个逻辑实体、13 条不变量、四类状态机、10 幅领域图与 18 个待验证场景。'
 pending=c.find(class_='pending')
 if pending:pending.decompose()
 if not c.find('a',href='docs/F03_Domain_and_Data_Model.html'):
  link=soup.new_tag('a',href='docs/F03_Domain_and_Data_Model.html');link.string='阅读 F03 HTML →';c.append(link)
  split=soup.new_tag('span');split['style']='margin:0 8px;color:#b1c3cf';split.string='|';c.append(split)
  md=soup.new_tag('a',href='docs/F03_Domain_and_Data_Model.md');md.string='查看 Markdown';c.append(md)
summary=soup.find(class_='summary')
if summary:
 boxes=summary.find_all('div',recursive=False)
 if len(boxes)>=2:
  boxes[1].clear()
  strong=soup.new_tag('strong');strong.string='F01 + F02 + F03';boxes[1].append(strong)
  small=soup.new_tag('small');small.string='F01 内容定稿；F02/F03 设计待具名批准';boxes[1].append(small)
head=soup.find('header')
if head:
 for tag in head.select('.strap span'):
  if 'F01 / F02' in tag.get_text(' ',strip=True):tag.string='F01 / F02 / F03 已形成设计文档 · 待批准'
foot=soup.find(class_='footer')
if foot:foot.string='IDP API Fabric · V1.0 工程设计编制中 · F01 内容定稿待签收 / F02–F03 待评审 · 尚未进入系统验收'
if soup.find(id='f03-assets') is None:
 target=next((h for h in soup.find_all('h2') if '双门户产品范围' in h.get_text()),None)
 assert target
 heading=soup.new_tag('h2',id='f03-assets');heading.string='F03 领域模型与工程追溯资产'
 content=BeautifulSoup('''<div class="panel"><div class="lanes"><div class="lane"><strong>领域事实与逻辑数据字典</strong><p>七个候选限界上下文、37 个机器可索引逻辑实体、13 条不变量、10 幅模型与流程图；Publication Target / Slot 粒度澄清待 ADR-0004 评审确认。</p><p><a href="docs/F03_Domain_and_Data_Model.html">F03 全文</a> · <a href="docs/F03_Logical_Model.csv">逻辑数据字典 CSV</a></p></div><div class="lane"><strong>需求追溯与领域验证</strong><p>映射 F01 全部 71 FR + 18 NFR；18 个领域验证场景全部为 NOT_RUN，没有冒充实际验证。</p><p><a href="tests/acceptance/F03_domain_trace.csv">需求/模型追溯</a> · <a href="tests/acceptance/F03_domain_scenarios.csv">设计场景</a></p></div></div></div>''','html.parser').div
 target.insert_before(heading);target.insert_before(content)
p.write_text(str(soup),encoding='utf-8')
print('Updated index to F01 + F02 + F03')
