#!/usr/bin/env python3
"""Activate F07 in the same index, preserving prior F01–F06 links and layout."""
from pathlib import Path
from bs4 import BeautifulSoup
ROOT=Path(__file__).resolve().parents[1];path=ROOT/'index.html';soup=BeautifulSoup(path.read_text('utf8'),'html.parser')
assert soup.find('main',class_='wrap'),'Unexpected index layout'
for c in soup.select('.grid section.card'):
 num=c.find(class_='num')
 if not num or num.get_text(strip=True)!='F07':continue
 h=c.find('h3');h.string='Engineering & Quality'
 desc=c.find('p');desc.string='Go DDD 工程分层、契约工具兼容、CAS/Outbox、权限、CI/CD、SLO/恢复与 42 个工程验证场景；9 幅图。'
 pending=c.find(class_='pending')
 if pending:pending.decompose()
 if c.find('a',href='docs/F07_Engineering_and_Quality.html') is None:
  link=soup.new_tag('a',href='docs/F07_Engineering_and_Quality.html');link.string='阅读 F07 HTML →';c.append(link)
  split=soup.new_tag('span');split['style']='margin:0 8px;color:#b1c3cf';split.string='|';c.append(split)
  md=soup.new_tag('a',href='docs/F07_Engineering_and_Quality.md');md.string='查看 Markdown';c.append(md)
 else:raise RuntimeError('F07 card not found')
summary=soup.find(class_='summary')
if summary:
 boxes=summary.find_all('div',recursive=False)
 if len(boxes)>=2:
  boxes[1].clear()
  strong=soup.new_tag('strong');strong.string='F01–F07';boxes[1].append(strong)
  small=soup.new_tag('small');small.string='F01 内容定稿；F02–F07 设计候选待具名评审，运行测试 NOT_RUN';boxes[1].append(small)
header=soup.find('header')
if header:
 for x in header.select('.strap span'):
  if '已形成' in x.get_text(' ',strip=True):x.string='F01–F07 已形成设计文档 · 待批准'
foot=soup.find(class_='footer')
if foot:foot.string='IDP API Fabric · V1.0 工程设计基线编制中 · F01–F07 已形成，F08 待编制 · 生产验收 NOT_RUN'
if soup.find(id='f07-assets') is None:
 target=next((h for h in soup.find_all('h2') if '双门户产品范围' in h.get_text()),None)
 if not target:raise RuntimeError('Cannot locate insertion point')
 heading=soup.new_tag('h2',id='f07-assets');heading.string='F07 · 工程质量与可执行审查资产'
 block=BeautifulSoup('''<div class="panel"><div class="lanes"><div class="lane"><strong>Go 工程实现与质量设计</strong><p>9 幅工程图，30 条候选工程控制，CAS/Outbox、双门户授权、CI/CD、容量与恢复。仅设计候选，真实运行未执行。</p><p><a href="docs/F07_Engineering_and_Quality.html">阅读 F07</a> · <a href="governance/policies/F07_Engineering_Controls_DRAFT.yaml">工程控制清单</a></p></div><div class="lane"><strong>工具兼容性与真实验收计划</strong><p>89 项 FR/NFR 设计追溯、15 项工具兼容候选及 42 个待执行工程验证场景，全部运行证据状态 NOT_RUN。</p><p><a href="tests/acceptance/F07_engineering_trace.csv">需求追溯</a> · <a href="tests/acceptance/F07_tool_compatibility_matrix.csv">工具矩阵</a> · <a href="tests/acceptance/F07_engineering_scenarios.csv">工程场景</a></p></div></div></div>''','html.parser').div
 target.insert_before(heading);target.insert_before(block)
path.write_text(str(soup),'utf8')
print('Index updated through F07, F08 remains pending')
