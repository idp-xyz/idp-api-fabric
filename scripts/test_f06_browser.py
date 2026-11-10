#!/usr/bin/env python3
"""F06 LOCAL_MOCK browser-only smoke. Equivalent inlined HTML/CSS to avoid restricted file:// URLs.
Requires Python playwright and a local Chromium browser. Does NOT test live auth, API, WCAG or product workflows.
"""
from pathlib import Path
from playwright.sync_api import sync_playwright
ROOT=Path(__file__).resolve().parents[1];OUT=Path(__import__('tempfile').gettempdir())/'idp_api_fabric_f06_browser';OUT.mkdir(exist_ok=True)
checks=[]
def check(cond,label):
 checks.append((label,bool(cond)))
 if not cond:print('FAIL',label)
with sync_playwright() as p:
 browser=p.chromium.launch(executable_path=(__import__('shutil').which('chromium') or __import__('shutil').which('google-chrome') or '/usr/bin/chromium'),headless=True,args=['--no-sandbox','--disable-gpu','--allow-file-access-from-files'])
 for filename,code in [('internal-engineering.html','IN'),('cargopai-developers.html','EX')]:
  for width,height,name in [(1440,950,'desktop'),(820,1060,'tablet'),(375,812,'mobile')]:
   page=browser.new_page(viewport={'width':width,'height':height},device_scale_factor=1)
   errors=[];page.on('pageerror',lambda err,errors=errors:errors.append(str(err)))
   html=(ROOT/'portal/prototypes'/filename).read_text('utf8').replace('<link rel="stylesheet" href="shared.css">','<style>'+ (ROOT/'portal/prototypes/shared.css').read_text('utf8') + '</style>');page.set_content(html,wait_until='load');page.wait_for_timeout(80)
   check(not errors,f'{code} {name} no pageerrors: {errors}')
   x=page.evaluate('document.documentElement.scrollWidth <= window.innerWidth+2')
   check(x,f'{code} {name} no horizontal page overflow ({page.evaluate("document.documentElement.scrollWidth")})')
   check(page.locator('#screen').inner_text().strip()!='',f'{code} {name} initial page has content')
   if name in ('desktop','mobile'):page.screenshot(path=str(OUT/f'{code}_{name}.png'),full_page=True)
   if name=='desktop':
    if code=='IN':
     page.locator('[data-page=catalog]').first.click();page.locator('#search').fill('tracking');check('Tracking Events' in page.locator('#screen').inner_text(),'IN catalog filter')
     page.locator('#product').select_option('IDP Parcel');check('工程治理工作台' in page.locator('#screen').inner_text(),'IN switch resets to scoped Dashboard')
     check('CargoPai' not in page.locator('#screen').inner_text(),'IN switch excludes CargoPai data')
     page.locator('[data-page=publication]').first.click();page.locator('[data-action=casConflict]').click();check(page.locator('#modalshade').get_attribute('class').find('open')>=0,'IN CAS confirmation modal')
     page.locator('#modalConfirm').click();check('409 CONFLICT' in page.locator('#toast').inner_text(),'IN CAS conflict toast')
     page.locator('[data-action=revoke]').click();page.locator('#modalConfirm').click();check('UNKNOWN / AT_RISK' in page.locator('#screen').inner_text(),'IN revoke at risk')
    else:
     page.locator('[data-page=reference]').first.click();check('Create Booking' in page.locator('#screen').inner_text(),'EX API operations visible')
     page.locator('[data-lang=go]').click();check('http.NewRequest' in page.locator('#codebox').inner_text(),'EX code language switch')
     page.locator('#versions').select_option('partner');check('Access denied' in page.locator('#screen').inner_text(),'EX deny private')
     check('Create Booking' not in page.locator('#screen').inner_text(),'EX private content hidden')
     page.locator('[data-page=sandbox]').first.click();check('NOT CONNECTED' in page.locator('#screen').inner_text(),'EX sandbox V1 only')
   if name=='mobile':
    page.locator('#navOpen').click();check('show' in page.locator('#sidebar').get_attribute('class'),f'{code} mobile nav open')
    page.keyboard.press('Escape');check('show' not in page.locator('#sidebar').get_attribute('class'),f'{code} mobile nav Escape')
   page.close()
 # F06 readme html render
 for width,height,label in [(1440,900,'desktop'),(375,800,'mobile')]:
  page=browser.new_page(viewport={'width':width,'height':height})
  page.set_content((ROOT/'docs/F06_Developer_Experience.html').read_text('utf8'),wait_until='load');page.wait_for_timeout(50)
  check(page.locator('figure.architecture-figure').count()==8,'F06 doc 8 figures '+label)
  check(page.evaluate('document.documentElement.scrollWidth <= innerWidth+2'),'F06 doc no page overflow '+label)
  page.screenshot(path=str(OUT/f'F06_{label}.png'),full_page=False)
  page.close()
 browser.close()
print('PLAYWRIGHT',sum(v for _,v in checks),'/',len(checks),'passed')
if not all(v for _,v in checks):exit(1)
