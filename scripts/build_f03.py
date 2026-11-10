#!/usr/bin/env python3
"""Render F03 from Markdown using same offline theme as F01/F02.
Raw HTML figures are kept in the Markdown authority; tables are accessible scroll regions.
"""
from pathlib import Path
from html import escape
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util
import re

ROOT=Path(__file__).resolve().parents[1]
SOURCE=ROOT/'docs/F03_Domain_and_Data_Model.md'
TARGET=ROOT/'docs/F03_Domain_and_Data_Model.html'

def load(name, path):
 spec=importlib.util.spec_from_file_location(name,path)
 mod=importlib.util.module_from_spec(spec)
 spec.loader.exec_module(mod)
 return mod
f01=load('f01_render', ROOT/'scripts/build_docs.py')
f02=load('f02_render', ROOT/'scripts/build_f02.py')
DOCUMENTS=[
 ('F01','Product Requirements','F01_Product_Requirements.html'),
 ('F02','System Architecture','F02_System_Architecture.html'),
 ('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),
 ('F04','Contract Governance','F04_API_Contract_Governance.html'),
 ('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),
 ('F06','Dual Portal Experience',None),
 ('F07','Engineering & Quality',None),
 ('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html'),
]
EXTRA='''<style>
.doc .architecture-figure{max-width:100%}.doc blockquote{border-left:4px solid #4b9baa;background:#f0f8fb;padding:12px 16px;border-radius:6px;margin:18px 0}
.doc code{overflow-wrap:anywhere}.doc h2{margin-top:39px}.doc .fig-lane{min-width:0}.doc table{min-width:760px}
@media(max-width:780px){.doc table{min-width:700px}.architecture-figure{overflow-wrap:anywhere}.fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:480px){.fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 9px}.doc .fig-mini{font-size:10px}}
@media print{.doc .architecture-figure{break-inside:avoid}.doc table{min-width:0;font-size:7.5pt}}
</style>'''
JS="""(()=>{const m=document.getElementById('menu'),q=document.getElementById('navsearch'),toc=document.getElementById('toc'),shade=document.getElementById('sideBackdrop');let last=null;function open(v){document.body.classList.toggle('navopen',v);m.setAttribute('aria-expanded',String(v));shade.setAttribute('aria-hidden',String(!v));if(v){last=document.activeElement;q.focus()}else if(last){last.focus();last=null}}m.addEventListener('click',()=>open(!document.body.classList.contains('navopen')));shade.addEventListener('click',()=>open(false));document.addEventListener('keydown',e=>{if(e.key==='Escape'&&document.body.classList.contains('navopen'))open(false)});toc.addEventListener('click',e=>{if(e.target.closest('a')&&document.body.classList.contains('navopen'))open(false)});q.addEventListener('input',()=>{let s=q.value.toLocaleLowerCase();toc.querySelectorAll('a').forEach(a=>a.style.display=a.textContent.toLocaleLowerCase().includes(s)?'':'none')});})();"""

def render():
 md=SOURCE.read_text(encoding='utf8')
 soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(md),'html.parser')
 if soup.find('h1'):soup.find('h1').decompose()
 for table in list(soup.select('table')):
  wrapper=soup.new_tag('div',attrs={'class':'table-scroll','role':'region','tabindex':'0','aria-label':'可左右滚动的领域模型与验收表格'})
  table.wrap(wrapper)
 headings=[]
 for h in soup.find_all(['h2','h3']):
  title=h.get_text(' ',strip=True)
  match=re.match(r'^(\d+(?:\.\d+)*)\b',title)
  anchor='section-'+match.group(1).replace('.','-') if match else f'heading-{len(headings)}'
  assert anchor not in [x[0] for x in headings],anchor
  h['id']=anchor
  headings.append((anchor,title,h.name))
 nav=''.join(f'<a href="#{escape(a)}" class="{"sub" if t=="h3" else ""}" style="{"padding-left:24px" if t=="h3" else ""}">{escape(s)}</a>' for a,s,t in headings)
 menu=''.join(f'<a href="{path}" class="{"active" if id=="F03" else ""}">{id} · {name}</a>' if path else f'<div class="pending">{id} · {name} <span class="mini">待编写</span></div>' for id,name,path in DOCUMENTS)
 figures=len(soup.select('figure.architecture-figure'))
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F03 V1.0 DDD领域模型与数据设计：Context Map、聚合、不变量、逻辑ERD、状态机、发布及恢复。"><title>F03 · IDP API Fabric 领域模型与数据设计</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F03 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{nav}</nav><div class="side-foot">V1.0 · 领域设计待具名评审<br>逻辑模型不是数据库 DDL；所有功能验证仍为 NOT_RUN。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　设计基线　/　F03</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F03_Domain_and_Data_Model.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F03 · Domain & Data Model</div><h1>IDP API Fabric<br>领域模型与数据设计</h1><p>DDD 限界上下文 · 逻辑 ERD · 聚合不变量 · 契约发布状态机 · 权限与故障恢复</p><div class="hero-meta"><span>V1.0 · 设计待评审</span><span>2026.11.09</span><span>正式工程目录</span><span>实现 / 测试未执行</span></div></header><div class="metrics"><div class="metric"><b>{figures}</b><span>专业领域与流程图</span></div><div class="metric"><b>13</b><span>聚合与事务不变量</span></div><div class="metric"><b>37</b><span>逻辑模型实体条目</span></div><div class="metric"><b>18</b><span>领域设计验证场景</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F03 · Generated from Markdown · Logical design candidate / runtime verification NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{JS}</script></body></html>'''
 TARGET.write_text(html,encoding='utf8')
 print('Built',TARGET.relative_to(ROOT),'figures',figures,'headings',len(headings))
 return figures
if __name__=='__main__':render()
