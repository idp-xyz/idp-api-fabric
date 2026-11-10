#!/usr/bin/env python3
"""Build self-contained F07 HTML from Markdown, preserving the F01–F06 visual grammar."""
from pathlib import Path
from html import escape
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util,re
ROOT=Path(__file__).resolve().parents[1]
SOURCE=ROOT/'docs/F07_Engineering_and_Quality.md';TARGET=ROOT/'docs/F07_Engineering_and_Quality.html'
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);obj=importlib.util.module_from_spec(spec);spec.loader.exec_module(obj);return obj
f01=load('f07_base1',ROOT/'scripts/build_docs.py')
f02=load('f07_base2',ROOT/'scripts/build_f02.py')
f03=load('f07_base3',ROOT/'scripts/build_f03.py')
EXTRA='''<style>
.doc .architecture-figure{max-width:100%;overflow-wrap:anywhere}
.doc .table-scroll{max-width:100%;overflow-x:auto;overscroll-behavior-x:contain}
.doc .table-scroll table{min-width:780px}
.doc h2{margin-top:43px}.doc blockquote{border-left:4px solid #227f8e;background:#edf7fa;padding:14px 18px;border-radius:8px}
.doc pre{overflow-x:auto;max-width:100%}.doc pre code{overflow-wrap:normal}.doc code{overflow-wrap:anywhere}
@media(max-width:780px){.doc .table-scroll table{display:table;width:max-content}.doc .fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:480px){.doc .fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 9px}}
@media print{.doc .table-scroll{overflow:visible}.doc .table-scroll table{min-width:0;width:100%;font-size:7.5pt}}
</style>'''
DOCS=[('F01','Product Requirements','F01_Product_Requirements.html'),('F02','System Architecture','F02_System_Architecture.html'),('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),('F04','Contract Governance','F04_API_Contract_Governance.html'),('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),('F06','Dual Portal Experience','F06_Developer_Experience.html'),('F07','Engineering & Quality','F07_Engineering_and_Quality.html'),('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')]
def render():
 soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(SOURCE.read_text('utf8')),'html.parser')
 h=soup.find('h1')
 if h:h.decompose()
 for table in list(soup.select('table')):
  w=soup.new_tag('div',attrs={'class':'table-scroll','tabindex':'0','role':'region','aria-label':'可横向滚动的 F07 工程设计与质量管理表'});table.wrap(w)
 heads=[];anchors=set()
 for heading in soup.find_all(['h2','h3']):
  label=heading.get_text(' ',strip=True);num=re.match(r'^(\d+(?:\.\d+)*)\b',label)
  anchor='section-'+num.group(1).replace('.','-') if num else f'heading-{len(heads)+1}'
  assert anchor not in anchors,anchor
  anchors.add(anchor);heading['id']=anchor;heads.append((anchor,label,heading.name))
 toc=''.join(f'<a href="#{escape(a)}" style="{"padding-left:24px" if typ=="h3" else ""}">{escape(label)}</a>' for a,label,typ in heads)
 menu=''.join(f'<a href="{url}" class="{"active" if code=="F07" else ""}">{code} · {label}</a>' if url else f'<div class="pending">{code} · {label} <span class="mini">待编写</span></div>' for code,label,url in DOCS)
 figs=len(soup.select('figure.architecture-figure'));assert figs==9,figs
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F07 V1.0 工程实现与质量设计：Go DDD、契约工具、CAS/Outbox、双门户服务端权限、CI/CD、运维质量与恢复演练。"><title>F07 · IDP API Fabric 工程实现与质量设计</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{f03.EXTRA}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">正式文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F07 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{toc}</nav><div class="side-foot">V1.0 · 工程设计候选 / 待具名批准<br>真实 Go / PostgreSQL / IAM / CDN / DR 测试均为 NOT_RUN。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　工程基线　/　F07</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F07_Engineering_and_Quality.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F07 · Engineering Implementation & Quality</div><h1>IDP API Fabric<br>工程实现与质量设计</h1><p>Go + DDD · Contract-First · Publication CAS · Outbox · Security-by-Design · CI/CD · SLI/SLO · Recovery</p><div class="hero-meta"><span>V1.0 · 工程设计候选</span><span>2026.11.09</span><span>继承 F01–F06</span><span>运行态验证 NOT_RUN</span></div></header><div class="metrics"><div class="metric"><b>{figs}</b><span>工程/质量可视化图</span></div><div class="metric"><b>30</b><span>候选工程控制</span></div><div class="metric"><b>42</b><span>待执行工程场景</span></div><div class="metric"><b>15</b><span>工具兼容验证项</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F07 · Markdown is authority · Real engineering/integration/production testing NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{f03.JS}</script></body></html>'''
 TARGET.write_text(html,'utf8');print(f'F07 generated: figures={figs}, headings={len(heads)}, bytes={TARGET.stat().st_size}')
if __name__=='__main__':render()
