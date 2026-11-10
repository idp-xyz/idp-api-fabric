#!/usr/bin/env python3
"""Build F04 offline HTML from the authoritative Markdown. No network access."""
from pathlib import Path
from html import escape
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util
import re
ROOT=Path(__file__).resolve().parents[1]
SRC=ROOT/'docs/F04_API_Contract_Governance.md'
OUT=ROOT/'docs/F04_API_Contract_Governance.html'
def mod(name,path):
 sp=importlib.util.spec_from_file_location(name,path)
 o=importlib.util.module_from_spec(sp);sp.loader.exec_module(o);return o
f01=mod('f01_for_f04', ROOT/'scripts/build_docs.py')
f02=mod('f02_for_f04', ROOT/'scripts/build_f02.py')
f03=mod('f03_for_f04', ROOT/'scripts/build_f03.py')
extra='''<style>
.doc .architecture-figure{max-width:100%}.doc .table-scroll{max-width:100%;overflow-x:auto}
.doc pre,.doc code{overflow-wrap:anywhere}.doc pre code{overflow-wrap:normal}.doc h2{margin-top:41px}
.doc blockquote{border-left:4px solid #4b9baa;background:#f0f8fb;padding:12px 16px;border-radius:6px}
@media(max-width:780px){.doc .table-scroll table{min-width:720px;width:max-content;display:table}.architecture-figure{overflow-wrap:anywhere}.fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:480px){.fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 9px}}
@media print{.doc .architecture-figure{break-inside:avoid}.doc .table-scroll table{min-width:0;width:100%}}
</style>'''

def render():
 soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(SRC.read_text(encoding='utf8')),'html.parser')
 h=soup.find('h1')
 if h:h.decompose()
 for table in list(soup.find_all('table')):
  wrap=soup.new_tag('div',attrs={'class':'table-scroll','role':'region','tabindex':'0','aria-label':'可左右滚动的 API 治理与测试表格'})
  table.wrap(wrap)
 headings=[]
 for tag in soup.find_all(['h2','h3']):
  name=tag.get_text(' ',strip=True)
  num=re.match(r'^(\d+(?:\.\d+)*)\b',name)
  anchor='section-'+num.group(1).replace('.','-') if num else f'heading-{len(headings)+1}'
  assert anchor not in [x[0] for x in headings],anchor
  tag['id']=anchor
  headings.append((anchor,name,tag.name))
 nav=''.join(f'<a href="#{escape(x)}" style="{"padding-left:24px" if typ=="h3" else ""}">{escape(name)}</a>' for x,name,typ in headings)
 docs=[('F01','Product Requirements','F01_Product_Requirements.html'),('F02','System Architecture','F02_System_Architecture.html'),('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),('F04','Contract Governance','F04_API_Contract_Governance.html'),('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),('F06','Dual Portal Experience',None),('F07','Engineering & Quality',None),('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')]
 menu=''.join(f'<a href="{url}" class="{"active" if code=="F04" else ""}">{code} · {label}</a>' if url else f'<div class="pending">{code} · {label} <span class="mini">待编写</span></div>' for code,label,url in docs)
 figs=len(soup.select('figure.architecture-figure'))
 assert figs==8,(figs,'figures')
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F04 V1.0 API 契约与治理规范：OpenAPI/AsyncAPI、Diff、发布控制、策略、审查、事件和可验证资产。"><title>F04 · IDP API Fabric API 契约与治理规范</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{f03.EXTRA}{extra}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F04 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{nav}</nav><div class="side-foot">V1.0 · 设计候选 / 待具名批准<br>示范 lint PASS 不是官方规范与生产安全验证通过。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　设计基线　/　F04</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F04_API_Contract_Governance.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F04 · API Contract Governance</div><h1>IDP API Fabric<br>API 契约与治理规范</h1><p>OpenAPI / AsyncAPI · 兼容性策略 · 不可变制品 · 安全发布 · 可执行规则 · 真实证据</p><div class="hero-meta"><span>V1.0 · 设计待评审</span><span>2026.11.09</span><span>正式工程目录</span><span>完整工具与业务验证 NOT_RUN</span></div></header><div class="metrics"><div class="metric"><b>{figs}</b><span>专业规则及流程图</span></div><div class="metric"><b>28</b><span>可机读候选治理规则</span></div><div class="metric"><b>24</b><span>待执行集成验证场景</span></div><div class="metric"><b>89</b><span>F01 FR / NFR 追溯项</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F04 · Generated from Markdown · Rule Registry is PROPOSED / production checks NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{f03.JS}</script></body></html>'''
 OUT.write_text(html,encoding='utf8')
 print('Built F04 HTML; figures',figs,'headings',len(headings))
if __name__=='__main__':render()
