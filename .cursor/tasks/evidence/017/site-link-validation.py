#!/usr/bin/env python3
"""Check compiled local links and registered route/fragment parity; no network required."""
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urljoin,urlsplit,unquote
import json,sys,re
site=Path(sys.argv[1] if len(sys.argv)>1 else '/tmp/okf017-site')
class Page(HTMLParser):
 def __init__(self):super().__init__();self.ids=set();self.links=[];self.nav=[];self.dynamic=False
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if 'id' in a:self.ids.add(a['id'])
  if tag=='a' and 'name' in a:self.ids.add(a['name'])
  if tag in ('a','link') and 'href' in a:self.links.append(('href',a['href']))
  if tag in ('img','script','iframe','source') and 'src' in a:self.links.append(('src',a['src']))
  if 'data-locale-switch' in a:self.nav.append(a.get('href'))
 def handle_data(self,data):
  if 'OKF_DATA' in data:self.dynamic=True
pages={};filepaths={}
for f in sorted(site.rglob('*.html')):
 p=Page();html=f.read_text();p.feed(html);data=re.search(r'<script id="okf-data" type="application/json">(.*?)</script>',html,re.S);p.dynamic=bool(data);p.concepts={c['id'] for c in json.loads(data.group(1))['concepts']} if data else set();rel=f.relative_to(site).as_posix();url='/okf/'+(rel[:-10] if rel.endswith('index.html') else rel);pages[url]=p;filepaths[url]=f
errors=[];checks=0;dynamic=[];external=0
for url,p in pages.items():
 for kind,href in p.links:
  parsed=urlsplit(urljoin('https://skosovsky.github.io'+url,href))
  if parsed.scheme not in ('http','https') or parsed.netloc!='skosovsky.github.io':external+=1;continue
  path=unquote(parsed.path)
  if not path.startswith('/okf/'):external+=1;continue
  relative=path[len('/okf/'):];dest=site/relative
  if path.endswith('/'):dest=dest/'index.html'
  elif dest.is_dir():dest=dest/'index.html'
  checks+=1
  if not dest.is_file():errors.append({'source':url,'link':href,'reason':'missing_file','resolved':path});continue
  fragment=unquote(parsed.fragment)
  if not fragment:continue
  destination=pages.get('/okf/'+(dest.relative_to(site).as_posix()[:-10] if dest.name=='index.html' else dest.relative_to(site).as_posix()))
  if destination is None:continue
  if fragment not in destination.ids:
   if destination.dynamic and fragment in destination.concepts:
    dynamic.append({'source':url,'link':href,'resolved':path,'fragment':fragment,'reason':'viewer concept route validated by viewer tests'})
   else:errors.append({'source':url,'link':href,'reason':'missing_fragment','resolved':path,'fragment':fragment})
registry=json.loads(Path('docs/_data/documentation.yml').read_text());pairs=[]
for d in registry['documents']:
 if not d['en']['url'].startswith('/'):continue
 en='/okf'+d['en']['url'];ru='/okf'+d['ru']['url'];e=pages.get(en);r=pages.get(ru)
 if e is None or r is None:errors.append({'document':d['id'],'reason':'missing_registry_route','en':en,'ru':ru});continue
 if e.nav!=[ru] or r.nav!=[en]:errors.append({'document':d['id'],'reason':'locale_switch','en_actual':e.nav,'ru_actual':r.nav,'en_expected':ru,'ru_expected':en})
 # All fragment IDs must exist in counterpart, including heading IDs and stable aliases.
 # Syntax-highlighter IDs are absent from these documents; footnote IDs still need parity.
 differences={'en_only':sorted(e.ids-r.ids),'ru_only':sorted(r.ids-e.ids)}
 if differences['en_only'] or differences['ru_only']:errors.append({'document':d['id'],'reason':'fragment_parity',**differences})
 pairs.append({'document':d['id'],'en':en,'ru':ru,'shared_fragments':len(e.ids&r.ids)})
report={'site_directory':str(site),'html_pages':len(pages),'local_link_checks':checks,'external_or_non_http_skipped':external,'paired_pages_checked':len(pairs),'pairs':pairs,'dynamic_viewer_routes':dynamic,'errors':errors}
Path('.cursor/tasks/evidence/017/site-link-validation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:report[k] for k in ('html_pages','local_link_checks','paired_pages_checked')}));print('errors:',len(errors));print(json.dumps(errors[:50],ensure_ascii=False,indent=2));sys.exit(bool(errors))
