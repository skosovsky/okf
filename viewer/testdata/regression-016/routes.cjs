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

function arrange(hash) {
  const ids = ['ordinary', 'fn:example', 'fnref:example', 'fnref1:example', 'тест'];
  const data = {
    concepts: ids.map(id => ({id, title: id, type: 'Note', trust: 'unknown', status: 'stable', staleness: 'unevaluated', search_text: id, sources: [],
      html: id === 'ordinary' ? '<a href="fn:example.md">fn</a><a href="fnref:example.md">fnref</a><a href="fnref1:example.md">numbered fnref concept</a><a href="#fn:1" id="fnref:1">footnote</a><a href="#fn:1" id="fnref1:1">repeated footnote</a><li id="fn:1"><a href="#fnref:1">backlink</a><a href="#fnref1:1">second backlink</a></li>' : ''})),
    edges: ids.slice(1, 4).map(id => ({kind: 'markdown', from_concept: 'ordinary', to_concept: id, raw: id + '.md', exists: true, from: 'ordinary', to: id})),
    spec_version: '0.2', reference_basis: 'unevaluated'
  };
  const nodes = Object.fromEntries(['okf-data', 'detail', 'list', 'search', 'type', 'show-graph', 'summary'].map(id => [id, new Element('div')]));
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
