#!/usr/bin/env python3
"""Render F02 Markdown into self-contained, offline, responsive HTML.
Uses the project's existing F01 CSS tokens to keep visual consistency.
No network access and no remote assets.
"""
from pathlib import Path
from html import escape
import importlib.util
import re
from markdown_it import MarkdownIt
from bs4 import BeautifulSoup

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('f01_builder',Path(__file__).with_name('build_docs.py'))
f01=importlib.util.module_from_spec(spec);spec.loader.exec_module(f01)
SOURCE=ROOT/'docs/F02_System_Architecture.md'
TARGET=ROOT/'docs/F02_System_Architecture.html'
EXTRA=r'''<style>
.table-scroll{width:100%;max-width:100%;overflow-x:auto;overscroll-behavior-x:contain;margin:16px 0 23px;border:1px solid #deebf0;border-radius:9px}.table-scroll table{margin:0;width:max-content;min-width:100%;border:0}.doc pre{max-width:100%;overflow-x:auto}.architecture-figure{border:1px solid #d0e1ec;border-radius:14px;padding:20px;margin:23px 0 28px;background:linear-gradient(135deg,#f8fbff,#f1f8fb);break-inside:avoid}
.fig-label{color:#47738f;font-size:11px;font-weight:800;letter-spacing:.09em;text-transform:uppercase;margin-bottom:14px}
.fig-stack{display:grid;gap:10px}.fig-head{background:linear-gradient(125deg,#123e5d,#117487);border-radius:10px;padding:18px 16px;text-align:center;color:#fff}.fig-head strong{display:block;color:#fff;font-size:17px}.fig-head small{display:block;font-size:11px;color:#cfe7ef;margin-top:5px}.fig-head.light{background:#e8f2f8;border:1px solid #cadae8;color:#1c4a63}.fig-head.light strong{color:#174f6c}.fig-head.light small{color:#4c7387}
.fig-grid{display:grid;gap:9px;align-items:stretch}.cols-2{grid-template-columns:repeat(2,minmax(0,1fr))}.cols-3{grid-template-columns:repeat(3,minmax(0,1fr))}.cols-4{grid-template-columns:repeat(4,minmax(0,1fr))}.cols-5{grid-template-columns:repeat(5,minmax(0,1fr))}
.fig-box{min-width:0;background:#fff;border:1px solid #c9dfea;border-radius:9px;padding:13px;text-align:center;box-shadow:0 3px 9px #18375209}.fig-box b{display:block;color:#19536f;font-size:12.2px;line-height:1.5}.fig-box small{display:block;color:#71889a;font-size:11px;margin-top:3px;line-height:1.5}.fig-primary{background:#dceef7;border-color:#75b1c9}.fig-primary b{font-size:13.5px;color:#124967}
.fig-box.tone-light{background:#f8fbfc}.fig-box.tone-green{background:#ecf7f2;border-color:#b8ded0}.fig-box.tone-green b{color:#1d7060}.fig-box.tone-red{background:#fff2ef;border-color:#e7c5bd}.fig-box.tone-red b{color:#a04941}
.fig-arrow,.fig-connect{text-align:center;color:#4c879e;font-weight:750;font-size:11.5px;line-height:1.6;padding:9px 2px}.fig-layer{display:flex;gap:14px;align-items:center;justify-content:center;background:white;border:1px solid #a2c5d6;border-radius:10px;padding:16px;flex-wrap:wrap}.fig-layer b{color:#125475;font-size:14px}.fig-layer span{font-size:12px;color:#5a788f}.fig-separator{border-top:2px dashed #bfd4df;text-align:center;color:#6a8293;padding:13px 10px 4px;font-size:11px;font-weight:700}
.fig-lane{display:flex;flex-direction:column;gap:8px;border:1px solid #c9dfea;border-radius:11px;background:#fff;padding:16px;min-width:0}.fig-lane strong{color:#154f71;font-size:15px;line-height:1.5}.fig-lane span{font-size:11px;color:#5e8299;border-bottom:1px solid #e0ebf0;padding-bottom:9px}.fig-lane small{color:#66869b;line-height:1.5}.fig-mini{padding:9px 10px;border:1px solid #deebf0;background:#f4f8fa;border-radius:6px;font-size:11px;color:#315f79}.architecture-figure figcaption{font-size:11px;color:#708b9f;line-height:1.7;margin-top:15px}
.fig-label+ .fig-grid{margin-top:3px}.architecture-figure .fig-grid+.fig-arrow{margin-top:5px}
.doc h2{counter-increment:foo}.doc blockquote strong{font-weight:750}.top-actions a{white-space:nowrap}
@media (max-width:1100px){.cols-5{grid-template-columns:repeat(3,1fr)}.cols-4{grid-template-columns:repeat(2,1fr)}}
@media (max-width:780px){.table-scroll table{display:table;min-width:650px;width:max-content}.architecture-figure{padding:13px 11px}.fig-head{padding:14px}.fig-head strong{font-size:15px}.fig-layer{display:block;text-align:center}.fig-layer b{display:block;margin-bottom:5px}.fig-box{padding:10px}.fig-box b{font-size:11.5px}.cols-3{grid-template-columns:repeat(2,1fr)}.cols-5{grid-template-columns:repeat(2,1fr)}}
@media (max-width:480px){.fig-grid.cols-2,.fig-grid.cols-3,.fig-grid.cols-4,.fig-grid.cols-5{grid-template-columns:1fr}.fig-lane{padding:12px}.fig-head strong{font-size:14px}.architecture-figure figcaption{font-size:10.5px}}
@media print{.architecture-figure{break-inside:avoid;background:#f6fafc}.fig-head,.fig-head.light{-webkit-print-color-adjust:exact;print-color-adjust:exact}.fig-grid.cols-4{grid-template-columns:repeat(4,1fr)}.fig-grid.cols-5{grid-template-columns:repeat(5,1fr)}}
</style>'''
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
def render():
 md=SOURCE.read_text(encoding='utf-8')
 renderer=MarkdownIt('default',{'html':True,'linkify':False}).enable('table')
 soup=BeautifulSoup(renderer.render(md),'html.parser')
 source_title=soup.find('h1')
 if source_title: source_title.decompose()  # visible hero is the document's single semantic H1
 for table in soup.select('table'):
  wrap=soup.new_tag('div');wrap['class']='table-scroll';wrap['role']='region';wrap['tabindex']='0'
  wrap['aria-label']='可横向滚动的数据表格'
  table.wrap(wrap)
 headings=[]
 for heading in soup.find_all(['h2','h3']):
  title=heading.get_text(' ',strip=True)
  num=re.match(r'^(\d+(?:\.\d+)*)\b',title)
  id_='section-'+num.group(1).replace('.','-') if num else f'heading-{len(headings)}'
  heading['id']=id_
  headings.append((id_,title,heading.name))
 # Explicit metadata header is in Markdown; HTML uses same.
 menu=''.join(f'<a href="{url}" class="{"active" if code=="F02" else ""}">{code} · {label}</a>' if url else f'<div class="pending">{code} · {label}<span class="mini">待编写</span></div>' for code,label,url in DOCUMENTS)
 nav='\n'.join(f'<a href="#{escape(id_)}" style="{"padding-left:24px" if level=="h3" else ""}">{escape(label)}</a>' for id_,label,level in headings)
 js="""(()=>{const menu=document.getElementById('menu'),q=document.getElementById('navsearch'),toc=document.getElementById('toc'),shade=document.getElementById('sideBackdrop');let last=null;function open(v){document.body.classList.toggle('navopen',v);menu.setAttribute('aria-expanded',String(v));shade.setAttribute('aria-hidden',String(!v));if(v){last=document.activeElement;q.focus()}else if(last){last.focus();last=null}}menu.addEventListener('click',()=>open(!document.body.classList.contains('navopen')));shade.addEventListener('click',()=>open(false));document.addEventListener('keydown',e=>{if(e.key==='Escape'&&document.body.classList.contains('navopen'))open(false)});toc.addEventListener('click',e=>{if(e.target.closest('a')&&document.body.classList.contains('navopen'))open(false)});q.addEventListener('input',()=>{let z=q.value.toLocaleLowerCase();toc.querySelectorAll('a').forEach(a=>a.style.display=a.textContent.toLocaleLowerCase().includes(z)?'':'none')});})();"""
 html=f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="description" content="IDP API Fabric V1.0 F02 系统架构设计，包含 C4、DDD、契约数据权威、双门户、安全发布与可靠性。"><title>F02 · IDP API Fabric 总体架构设计</title>{f01.STYLE}{f01.EXTRA_STYLE}{EXTRA}</head><body><a class="skip-link" href="#main-content">跳到文档正文</a><button id="sideBackdrop" class="side-backdrop" aria-label="关闭章节目录" aria-hidden="true" tabindex="-1"></button><div class="layout"><aside class="side" id="sidebar"><a href="../index.html" style="text-decoration:none"><div class="logo">IDP API Fabric<small>ENGINEERING BASELINE</small></div></a><div class="side-kicker">文档体系</div><div class="navlist">{menu}</div><hr><div class="side-kicker">F02 · 目录</div><input id="navsearch" type="search" placeholder="筛选本文章节…" aria-label="筛选目录章节"><nav class="navlist" id="toc">{nav}</nav><div class="side-foot">V1.0 · 设计候选 / 待批准<br>文档中所有 PoC、测试与门禁均尚未实际执行。</div></aside><div class="main"><header class="topbar"><div><button class="menu" id="menu" aria-label="展开目录" aria-controls="sidebar" aria-expanded="false">☰ 目录</button><span class="crumb"><strong>IDP API Fabric</strong>　/　设计基线　/　F02</span></div><div class="top-actions"><a class="desktop-only" href="../index.html">总目录</a><a href="F02_System_Architecture.md">Markdown 源文件</a><button onclick="window.print()">打印 / PDF</button></div></header><main id="main-content" class="sheet" tabindex="-1"><header class="hero"><div class="eyebrow">F02 · System Architecture</div><h1>IDP API Fabric<br>总体架构设计</h1><p>跨产品联邦治理 · Go 模块化 DDD · 不可变契约发布 · 安全多租户 · 双门户架构</p><div class="hero-meta"><span>V1.0 · 设计候选基线</span><span>2026.11.09</span><span>内部架构文档</span><span>PoC / 门禁未执行</span></div></header><div class="metrics"><div class="metric"><b>8</b><span>关键架构图</span></div><div class="metric"><b>6</b><span>设计 ADR（含 F01 继承）</span></div><div class="metric"><b>12</b><span>架构验证场景</span></div><div class="metric"><b>20</b><span>正文架构章节（0–19）</span></div></div><article class="doc">{str(soup)}</article><div class="footer">IDP API Fabric · F02 · Generated from Markdown · Design candidate, approval and runtime verification pending</div></main></div></div><a href="#" class="back-to-top">↑ 返回顶部</a><script>{js}</script></body></html>'''
 TARGET.write_text(html,encoding='utf-8')
 print(f'Built {TARGET.relative_to(ROOT)}; headings={len(headings)}, figures={len(soup.select("figure.architecture-figure"))}')
if __name__=='__main__':render()
