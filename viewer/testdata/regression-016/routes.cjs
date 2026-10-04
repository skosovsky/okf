// Executes the production asset. This small DOM is a regression harness, not
// evidence of browser acceptance; native history/scroll are checked separately.
'use strict';
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const path = require('node:path');
const script = fs.readFileSync(path.join(__dirname, '../../assets/viewer.js'), 'utf8');

class Element {
  constructor(tag) {
    this.tag = tag; this.children = []; this.dataset = {}; this.events = {};
    this.attrs = {}; this.style = {}; this.value = ''; this.checked = false;
    this.classList = {toggle() {}};
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  addEventListener(type, callback) { this.events[type] = callback; }
  getAttribute(name) { return this.attrs[name]; }
  setAttribute(name, value) { this.attrs[name] = value; }
  removeAttribute(name) { delete this.attrs[name]; }
  set href(value) { this.attrs.href = value; }
  get href() { return this.attrs.href; }
  scrollIntoView() { this.scrolled = true; }
  set innerHTML(html) {
    this.children = [];
    for (const match of html.matchAll(/<(a|li)\b([^>]*)>/g)) {
      const child = new Element(match[1]);
      for (const attr of match[2].matchAll(/(id|href)="([^"]*)"/g)) {
        child.attrs[attr[1]] = attr[2];
        if (attr[1] === 'id') child.id = attr[2];
      }
      this.append(child);
    }
  }
  querySelectorAll(selector) {
    const result = [];
    for (const child of this.children) {
      if (selector === '[id]' ? child.id : child.tag === selector) result.push(child);
      result.push(...child.querySelectorAll(selector));
    }
    return result;
  }
}

function arrange(hash, locale = 'en', empty = false, customize = () => {}) {
  const ids = ['ordinary', 'fn:example', 'fnref:example', 'fnref1:example', 'тест'];
  const data = {
    concepts: (empty ? [] : ids).map(id => ({id, title: id, type: 'Note', trust: 'unknown', status: 'stable', staleness: 'unevaluated', search_text: id, sources: [],
      html: id === 'ordinary' ? '<a href="fn:example.md">fn</a><a href="fnref:example.md">fnref</a><a href="fnref1:example.md">numbered fnref concept</a><a href="#fn:1" id="fnref:1">footnote</a><a href="#fn:1" id="fnref1:1">repeated footnote</a><li id="fn:1"><a href="#fnref:1">backlink</a><a href="#fnref1:1">second backlink</a></li>' : ''})),
    edges: ids.slice(1, 4).map(id => ({kind: 'markdown', from_concept: 'ordinary', to_concept: id, raw: id + '.md', exists: true, from: 'ordinary', to: id})),
    spec_version: '0.2', reference_basis: 'unevaluated'
  };
  customize(data);
  const nodes = Object.fromEntries(['okf-data', 'detail', 'list', 'search', 'type', 'show-graph', 'summary', 'language', 'viewer-title', 'search-label', 'type-label', 'all-types', 'graph-label', 'language-label'].map(id => [id, new Element('div')]));
  nodes['okf-data'].textContent = JSON.stringify(data);
  const events = {};
  const history = [hash]; let position = 0;
  const location = {
    get hash() { return history[position]; },
    set hash(value) {
      history.splice(position + 1); history.push('#' + value.replace(/^#/, '')); position++;
      events.hashchange();
    }
  };
  const document = {
    documentElement: {lang: locale},
    getElementById(id) { return nodes[id] || nodes.detail.querySelectorAll('[id]').find(node => node.id === id); },
    createElement(tag) { return new Element(tag); }, createElementNS(ns, tag) { return new Element(tag); }
  };
  vm.runInNewContext(script, {document, location, addEventListener(type, callback) { events[type] = callback; }});
  return {
    nodes, location,
    back() { if (position) { position--; events.hashchange(); } },
    forward() { if (position + 1 < history.length) { position++; events.hashchange(); } },
    current() { return nodes.detail.children.find(node => node.tag === 'code').textContent; }
  };
}

// Arrange / Act / Assert: direct encoded routes must render their own cards.
for (const id of ['fn:example', 'fnref:example', 'fnref1:example', 'тест', 'ordinary']) {
  const page = arrange('#' + encodeURIComponent(id));
  assert.equal(page.current(), id);
}

// Arrange: ordinary card includes concept links and real footnote markup.
const page = arrange('#ordinary');
// Act / Assert: a concept link is rewritten into an encoded concept route.
const fnLink = page.nodes.detail.querySelectorAll('a').find(a => a.href === '#fn%3Aexample');
assert.ok(fnLink);
page.location.hash = fnLink.href;
assert.equal(page.current(), 'fn:example');
page.nodes.list.querySelectorAll('button').find(b => b.dataset.id === 'fnref:example').events.click();
assert.equal(page.current(), 'fnref:example');
// A repeated-reference-shaped ConceptID must also be a concept link, not an anchor.
const repeatedPage = arrange('#ordinary');
const repeatedConceptLink = repeatedPage.nodes.detail.querySelectorAll('a').find(a => a.href === '#fnref1%3Aexample');
assert.ok(repeatedConceptLink);
repeatedPage.location.hash = repeatedConceptLink.href;
assert.equal(repeatedPage.current(), 'fnref1:example');
repeatedPage.nodes.list.querySelectorAll('button').find(b => b.dataset.id === 'ordinary').events.click();
repeatedPage.nodes.list.querySelectorAll('button').find(b => b.dataset.id === 'fnref1:example').events.click();
assert.equal(repeatedPage.current(), 'fnref1:example');
page.back(); assert.equal(page.current(), 'fn:example');
page.back(); assert.equal(page.current(), 'ordinary');
page.forward(); assert.equal(page.current(), 'fn:example');
page.location.hash = '#ordinary';

// Act / Assert: footnote and backlink scroll locally without route mutation.
for (const href of ['#fn:1', '#fnref:1', '#fnref1:1']) {
  let prevented = false;
  const link = page.nodes.detail.querySelectorAll('a').find(a => a.href === href);
  link.events.click({preventDefault() { prevented = true; }});
  assert.ok(prevented);
  assert.equal(page.location.hash, '#ordinary');
  assert.ok(page.nodes.detail.querySelectorAll('[id]').find(a => a.id === href.slice(1)).scrolled);
}

// Act / Assert: a genuine anchor hash retains the card; absent anchors fallback.
page.location.hash = '#fn:1'; assert.equal(page.current(), 'ordinary');
page.location.hash = '#fnref1:1'; assert.equal(page.current(), 'ordinary');
page.location.hash = '#fnref%3Aexample'; assert.equal(page.current(), 'fnref:example');
page.location.hash = '#fn:missing'; assert.equal(page.current(), 'ordinary');
page.location.hash = '#%E0%A4%A'; assert.equal(page.current(), 'ordinary');
console.log('Viewer routes: direct links, clicks, simulated history, footnotes and backlinks passed.');

function text(node) { return (node.textContent || '') + node.children.map(text).join(' '); }
// Arrange: authored English/custom type stays exactly as supplied.
const ru = arrange('#ordinary', 'ru');
assert.equal(ru.nodes['viewer-title'].textContent, 'Просмотр знаний OKF');
assert.ok(text(ru.nodes.detail).includes('Источники'));
assert.ok(text(ru.nodes.detail).includes('unknown')); // unknown trust stays raw
assert.ok(text(ru.nodes.detail).includes('Note'));
assert.ok(text(ru.nodes.detail).includes('стабильно'));
ru.nodes.search.value = 'ordinary';
ru.nodes.type.value = 'Note';
ru.nodes['show-graph'].checked = true;
// Act: switch without navigation or losing filters.
ru.nodes.language.value = 'en'; ru.nodes.language.events.change();
assert.equal(ru.current(), 'ordinary');
assert.equal(ru.location.hash, '#ordinary');
assert.equal(ru.nodes.search.value, 'ordinary');
assert.equal(ru.nodes.type.value, 'Note');
assert.equal(ru.nodes['show-graph'].checked, true);
assert.ok(text(ru.nodes.detail).includes('Local graph'));
ru.nodes.language.value = 'ru'; ru.nodes.language.events.change();
assert.ok(text(ru.nodes.detail).includes('Граф соседних заметок'));
// A genuine footnote route retains the selected card during locale refresh.
ru.location.hash = '#fn:1';
ru.nodes.language.value = 'en'; ru.nodes.language.events.change();
assert.equal(ru.current(), 'ordinary');
assert.ok(text(ru.nodes.detail).includes('Sources'));
// Reload initializes the exported language, not the previous runtime choice.
assert.equal(arrange('#ordinary', 'ru').nodes.language.value, 'ru');
const emptyRu = arrange('', 'ru', true);
assert.ok(text(emptyRu.nodes.detail).includes('В этом наборе нет заметок.'));
assert.ok(text(emptyRu.nodes.list).includes('Подходящих заметок нет.'));
console.log('Viewer language: defaults, switching, state retention, empty states and authored values passed.');

// Known labels are translated only in their own category; authored source
// titles, custom status values and hostile URLs stay data, never markup.
for (const [field, values] of Object.entries({trust:['unverified','machine-confirmed','human-reviewed'],status:['draft','stable','deprecated','unresolved'],staleness:['fresh','stale','invalid','unevaluated']})) {
  for (const value of values) {
    const translated = arrange('#ordinary','ru',false,data => {data.concepts[0][field] = value;});
    assert.ok(!text(translated.nodes.detail).includes(': ' + value), field + ': ' + value);
  }
}
const custom = arrange('#ordinary','ru',false,data => {
 data.concepts[0].status = 'markdown';
 data.concepts[0].sources = [{title:'stable',id:'custom',resource:'javascript:alert(1)'}];
 data.edges.push({kind:'custom_kind',from_concept:'ordinary',to_concept:'missing',from:'ordinary',to:'missing',exists:false});
});
assert.ok(text(custom.nodes.detail).includes('состояние: markdown'));
assert.ok(text(custom.nodes.detail).includes('stable'));
assert.ok(text(custom.nodes.detail).includes('custom_kind'));
assert.ok(text(custom.nodes.detail).includes('(отсутствует)'));
assert.ok(!custom.nodes.detail.querySelectorAll('a').some(a => /^javascript:/.test(a.href || '')));
const footnoteEvent = arrange('#ordinary');
// Browser supplies an Event to hashchange. It must retain anchor behavior.
footnoteEvent.location.hash = '#fn:1';
assert.equal(footnoteEvent.current(),'ordinary');
console.log('Viewer locale values and hostile source resource passed.');

// Russian count forms cover singular, paucal, plural and 11–14 exceptions.
for (const [count, note, edge] of [[0,'заметок','связей'],[1,'заметка','связь'],[2,'заметки','связи'],[4,'заметки','связи'],[5,'заметок','связей'],[11,'заметок','связей'],[12,'заметок','связей'],[14,'заметок','связей'],[21,'заметка','связь'],[22,'заметки','связи'],[25,'заметок','связей'],[111,'заметок','связей']]) {
 const counted = arrange('#ordinary','ru',false,data => {
  data.concepts = Array.from({length:count},(_,i) => ({...data.concepts[0],id:i === 0 ? 'ordinary' : 'note'+i}));
  data.edges = Array.from({length:count},() => ({kind:'custom',from_concept:'ordinary',to_concept:'missing',from:'ordinary',to:'missing',exists:false}));
 });
 assert.ok(counted.nodes.summary.textContent.startsWith(count+' '+note+' · '+count+' '+edge),counted.nodes.summary.textContent);
 if(count) {
  counted.nodes['show-graph'].checked = true; counted.nodes['show-graph'].events.change();
  assert.ok(text(counted.nodes.detail).includes('Граф соседних заметок · '+count+' '+edge));
 }
}
console.log('Russian count forms for notes, edges and graph connections passed.');
