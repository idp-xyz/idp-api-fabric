#!/usr/bin/env python3
"""Render F05 authoritative Markdown as self-contained offline HTML; does not execute security tests."""
from pathlib import Path
from html import escape
import re
from bs4 import BeautifulSoup
from markdown_it import MarkdownIt
import importlib.util
ROOT=Path(__file__).resolve().parents[1]
SOURCE=ROOT/'docs/F05_Security_and_Multitenancy.md'
TARGET=ROOT/'docs/F05_Security_and_Multitenancy.html'
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path); mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod);return mod
f01=load('f01_05',ROOT/'scripts/build_docs.py')
f02=load('f02_05',ROOT/'scripts/build_f02.py')
f03=load('f03_05',ROOT/'scripts/build_f03.py')
EXTRA='''<style>
.doc .architecture-figure{max-width:100%;}.doc .table-scroll{max-width:100%;overflow-x:auto;overscroll-behavior-x:contain}
.doc h2{margin-top:43px}.doc blockquote{border-left:4px solid #35899e;background:#f0f8fb;padding:14px 19px;border-radius:7px}
.doc code{overflow-wrap:anywhere}.doc pre code{overflow-wrap:normal}.doc .fig-grid{gap:10px}
@media(max-width:780px){.doc .table-scroll table{min-width:760px;width:max-content;display:table}.doc .fig-grid.cols-3{grid-template-columns:repeat(2,minmax(0,1fr))}.doc .architecture-figure{overflow-wrap:anywhere}}
@media(max-width:480px){.doc .fig-grid.cols-3{grid-template-columns:1fr}.doc .architecture-figure{padding:12px 10px}}
@media print{.doc .architecture-figure{break-inside:avoid}.doc .table-scroll{overflow:visible}.doc .table-scroll table{min-width:0;width:100%;font-size:7.5pt}}
</style>'''
DOCS=[('F01','Product Requirements','F01_Product_Requirements.html'),('F02','System Architecture','F02_System_Architecture.html'),('F03','Domain & Data Model','F03_Domain_and_Data_Model.html'),('F04','Contract Governance','F04_API_Contract_Governance.html'),('F05','Security & Multitenancy','F05_Security_and_Multitenancy.html'),('F06','Dual Portal Experience',None),('F07','Engineering & Quality',None),('F08','Delivery & Acceptance','F08_Delivery_and_Acceptance.html')]
def render():
 soup=BeautifulSoup(MarkdownIt('default',{'html':True}).enable('table').render(SOURCE.read_text(encoding='utf-8')),'html.parser')
 if soup.find('h1'):soup.find('h1').decompose()
 for t in list(soup.select('table')):
  w=soup.new_tag('div',attrs={'class':'table-scroll','role':'region','tabindex':'0','aria-label':'可横向滚动的安全规则和风险表格'});t.wrap(w)
 heads=[]
 for heading in soup.find_all(['h2','h3']):
  label=heading.get_text(' ',strip=True);num=re.match(r'^(\d+(?:\.\d+)*)\b',label)
  anchor='section-'+num.group(1).replace('.','-') if num else f'heading-{len(heads)+1}'
  assert anchor not in [x[0] for x in heads],anchor
  heading['id']=anchor;heads.append((anchor,label,heading.name))
 toc=''.join(f'<a href="#{escape(a)}" style="{"padding-left:24px" if typ=="h3" else ""}">{escape(label)}</a>' for a,label,typ in heads)
 menu=''.join(f'<a href="{url}" class="{"active" if code=="F05" else ""}">{code} · {label}</a>' if url else f'<div class="pending">{code} · {label} <span class="mini">待编写</span></div>' for code,label,url in DOCS)
 figs=len(soup.select('figure.architecture-figure'))
 assert figs==10,figs
 n_controls=35;n_threat=29;n_scenes=36
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric F05 V1.0 安全架构与多租户设计：身份联邦、STRIDE、ABAC、授权矩阵、访问隔离、发布撤销及负向安全验证。"><title>F05 · IDP API Fabric 安全架构与多租户设计</title>{f01.STYLE}{f01.EXTRA_STYLE}{f02.EXTRA}{f03.EXTRA}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F05 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{toc}</nav><div class="side-foot">V1.0 · 安全设计候选 / 待具名批准<br>设计矩阵与静态检查不等于渗透测试或合规认证。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　设计基线　/　F05</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F05_Security_and_Multitenancy.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F05 · Security & Multitenancy</div><h1>IDP API Fabric<br>安全架构与多租户设计</h1><p>身份联邦 · 多维授权 · STRIDE 威胁模型 · 安全发布与撤销 · 双门户隔离 · 可审计证据</p><div class="hero-meta"><span>V1.0 · 设计待评审</span><span>2026.11.09</span><span>正式工程目录</span><span>所有真实安全场景 NOT_RUN</span></div></header><div class="metrics"><div class="metric"><b>{figs}</b><span>安全模型及业务流程图</span></div><div class="metric"><b>{n_controls}</b><span>操作级授权规则</span></div><div class="metric"><b>{n_threat}</b><span>STRIDE 风险条目</span></div><div class="metric"><b>{n_scenes}</b><span>待执行安全场景</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F05 · Generated from Markdown · Security design candidate / runtime & penetration testing NOT_RUN</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{f03.JS}</script></body></html>'''
 TARGET.write_text(html,encoding='utf8')
 print(f'Built F05 HTML; figures={figs} headings={len(heads)}')
if __name__=='__main__':render()
