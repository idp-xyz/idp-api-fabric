#!/usr/bin/env python3
"""Render F08 from single Markdown authority, using the existing offline design system."""
from pathlib import Path
from html import escape
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util,re
ROOT=Path(__file__).resolve().parents[1]
SRC=ROOT/'docs/F08_Delivery_and_Acceptance.md'; OUT=ROOT/'docs/F08_Delivery_and_Acceptance.html'
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
f01=load('base_f01_08',ROOT/'scripts/build_docs.py')
f02=load('base_f02_08',ROOT/'scripts/build_f02.py')
f03=load('base_f03_08',ROOT/'scripts/build_f03.py')
DOCS=[('F01','Product Requirements','F01_Product_Requirements.html'),('F02','System Architecture','F02_System_Architecture.html'),('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),('F04','Contract Governance','F04_API_Contract_Governance.html'),('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),('F06','Dual Portal Experience','F06_Developer_Experience.html'),('F07','Engineering & Quality','F07_Engineering_and_Quality.html'),('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')]
EXTRA='''<style>
.doc .architecture-figure{max-width:100%;overflow-wrap:anywhere}
.doc .table-scroll{max-width:100%;overflow-x:auto;overscroll-behavior-x:contain}
.doc .table-scroll table{min-width:760px}
.doc h2{margin-top:45px}.doc blockquote{border-left:4px solid #227f8e;background:#edf7fa;padding:14px 18px;border-radius:8px}
.doc pre{max-width:100%;overflow-x:auto}.doc code{overflow-wrap:anywhere}
@media(max-width:780px){.doc .table-scroll table{display:table;width:max-content}.doc .fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:480px){.doc .fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 9px}}
@media print{.doc .table-scroll{overflow:visible}.doc .table-scroll table{min-width:0;width:100%;font-size:7.5pt}}
</style>'''
def render():
 src=SRC.read_text('utf8');soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(src),'html.parser')
 h=soup.find('h1')
 if h:h.decompose()
 for table in list(soup.find_all('table')):
  wrap=soup.new_tag('div',attrs={'class':'table-scroll','role':'region','tabindex':'0','aria-label':'可横向滚动的 F08 实施与验收表格'})
  table.wrap(wrap)
 anchors=set();heads=[]
 for h in soup.find_all(['h2','h3']):
  title=h.get_text(' ',strip=True);m=re.match(r'^(\d+(?:\.\d+)*)\b',title)
  anchor='section-'+m.group(1).replace('.','-') if m else f'heading-{len(heads)+1}'
  assert anchor not in anchors,anchor
  h['id']=anchor;anchors.add(anchor);heads.append((anchor,title,h.name))
 toc=''.join(f'<a href="#{escape(a)}" style="{"padding-left:24px" if level=="h3" else ""}">{escape(title)}</a>' for a,title,level in heads)
 menu=''.join(f'<a href="{path}" class="{"active" if num=="F08" else ""}">{num} · {name}</a>' for num,name,path in DOCS)
 figs=len(soup.select('figure.architecture-figure'));assert figs==9,figs
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F08 V1.0 实施路线图与验收规范：WBS、真实证据、六道门禁、双门户、Go/No-Go 与上线恢复。"><title>F08 · IDP API Fabric 实施路线图与验收规范</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{f03.EXTRA}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">正式文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F08 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{toc}</nav><div class="side-foot">V1.0 · 交付与验收设计候选<br>工程/安全/运营真实验收 NOT_RUN；批准人员 TBD。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　工程设计基线　/　F08</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F08_Delivery_and_Acceptance.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F08 · Delivery Roadmap & Acceptance</div><h1>IDP API Fabric<br>实施路线图与验收规范</h1><p>有序交付 · 业务纵切 · MVP / V1 · STD-CP1–CP6 · 真实证据与发布责任</p><div class="hero-meta"><span>V1.0 · 设计候选</span><span>2026.11.09</span><span>继承 F01–F07</span><span>运行态验证 NOT_RUN</span></div></header><div class="metrics"><div class="metric"><b>12</b><span>实施工作包</span></div><div class="metric"><b>10</b><span>验收里程碑</span></div><div class="metric"><b>24</b><span>首批关键验收场景</span></div><div class="metric"><b>{figs}</b><span>工程与交付示意图</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F08 · Markdown is authority · Documentation design complete candidate / real testing NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{f03.JS}</script></body></html>'''
 OUT.write_text(html,'utf8');print('F08 HTML built',len(heads),'headings',figs,'figures',OUT.stat().st_size,'bytes')
if __name__=='__main__':render()
