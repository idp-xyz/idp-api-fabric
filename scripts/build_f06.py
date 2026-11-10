#!/usr/bin/env python3
"""F06 single-source Markdown -> self-contained offline HTML; prototype assets are separate local HTML."""
from pathlib import Path
from html import escape
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util,re
ROOT=Path(__file__).resolve().parents[1]
SOURCE=ROOT/'docs/F06_Developer_Experience.md';TARGET=ROOT/'docs/F06_Developer_Experience.html'
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);o=importlib.util.module_from_spec(spec);spec.loader.exec_module(o);return o
f01=load('F06base1',ROOT/'scripts/build_docs.py')
f02=load('F06base2',ROOT/'scripts/build_f02.py')
f03=load('F06base3',ROOT/'scripts/build_f03.py')
EXTRA='''<style>
.doc .architecture-figure{max-width:100%;overflow-wrap:anywhere}.doc .table-scroll{max-width:100%;overflow-x:auto;overscroll-behavior-x:contain}.doc .table-scroll table{min-width:770px}.doc h2{margin-top:43px}
.doc blockquote{border-left:4px solid #258b92;background:#edf9f8;padding:15px 18px;border-radius:8px}.doc code{overflow-wrap:anywhere}.doc pre code{overflow-wrap:normal}
@media(max-width:780px){.doc .table-scroll table{width:max-content;display:table}.doc .fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:480px){.doc .fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 9px}}
@media print{.doc .table-scroll{overflow:visible}.doc .table-scroll table{min-width:0;width:100%;font-size:7.5pt}}
</style>'''
DOCS=[('F01','Product Requirements','F01_Product_Requirements.html'),('F02','System Architecture','F02_System_Architecture.html'),('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),('F04','Contract Governance','F04_API_Contract_Governance.html'),('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),('F06','Dual Portal Experience','F06_Developer_Experience.html'),('F07','Engineering & Quality',None),('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')]
def render():
 soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(SOURCE.read_text('utf8')),'html.parser')
 h=soup.find('h1')
 if h:h.decompose()
 for table in list(soup.select('table')):
  w=soup.new_tag('div',attrs={'class':'table-scroll','tabindex':'0','role':'region','aria-label':'可横向滚动的 F06 页面与体验规范表'});table.wrap(w)
 headings=[]
 for h in soup.find_all(['h2','h3']):
  label=h.get_text(' ',strip=True);num=re.match(r'^(\d+(?:\.\d+)*)\b',label)
  anchor='section-'+num.group(1).replace('.','-') if num else f'heading-{len(headings)+1}'
  assert anchor not in [x[0] for x in headings],anchor
  h['id']=anchor;headings.append((anchor,label,h.name))
 toc=''.join(f'<a href="#{escape(a)}" style="{"padding-left:24px" if typ=="h3" else ""}">{escape(label)}</a>' for a,label,typ in headings)
 menu=''.join(f'<a href="{url}" class="{"active" if code=="F06" else ""}">{code} · {label}</a>' if url else f'<div class="pending">{code} · {label} <span class="mini">待编写</span></div>' for code,label,url in DOCS)
 count=len(soup.select('figure.architecture-figure'));assert count==8,count
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F06 V1.0 双门户产品与体验设计：内部门户、CargoPai Developers、共享设计系统、可操作 HTML 原型。"><title>F06 · IDP API Fabric 双门户产品与体验设计</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{f03.EXTRA}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">正式文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F06 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{toc}</nav><div class="side-foot">V1.0 · 设计候选 / 待具名审查<br>交互原型全为 LOCAL MOCK；实际服务测试 NOT_RUN。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　设计基线　/　F06</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F06_Developer_Experience.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F06 · Dual Portal Product & Experience Design</div><h1>IDP API Fabric<br>双门户产品与体验设计</h1><p>F06-A Engineering Portal · F06-B CargoPai Developers · F06-C Shared Design System</p><div class="hero-meta"><span>V1.0 · 设计待评审</span><span>2026.11.09</span><span>正式工程目录</span><span>真实运行验证 NOT_RUN</span></div></header><div class="metrics"><div class="metric"><b>2</b><span>可操作的独立门户原型</span></div><div class="metric"><b>{count}</b><span>用户旅程与交互模型图</span></div><div class="metric"><b>21</b><span>页面 / 共享视图目录</span></div><div class="metric"><b>32</b><span>待执行体验验收场景</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F06 · Markdown is authority · All prototypes are LOCAL_MOCK, runtime tests NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{f03.JS}</script></body></html>'''
 TARGET.write_text(html,'utf8');print(f'F06 generated: {count} figures, {len(headings)} headings, {TARGET.stat().st_size} bytes')
if __name__=='__main__':render()
