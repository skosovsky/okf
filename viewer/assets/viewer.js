/* OKF viewer assets: original work; no third-party dependencies or fetches. */
(() => {
  'use strict';
  const data = JSON.parse(document.getElementById('okf-data').textContent);
  const concepts = new Map(data.concepts.map(c => [c.id, c]));
  const byId = document.getElementById('detail');
  const list = document.getElementById('list');
  const search = document.getElementById('search');
  const type = document.getElementById('type');
  const showGraph = document.getElementById('show-graph');
  const summary = document.getElementById('summary');
  summary.textContent = data.concepts.length + ' concepts · ' + data.edges.length + ' edges · OKF ' + data.spec_version + ' · staleness: ' + data.reference_basis + (data.reference_time ? ' (' + data.reference_time + ')' : '');
  [...new Set(data.concepts.map(c => c.type).filter(Boolean))].sort().forEach(t => {
    const option = document.createElement('option'); option.value = t; option.textContent = t; type.append(option);
  });
  const el = (tag, cls, content) => {
    const item = document.createElement(tag); if (cls) item.className = cls;
    if (content != null) item.textContent = content; return item;
  };
  const navigate = id => { location.hash = encodeURIComponent(id); };
  const edgeRow = (e, outgoing) => {
    const target = outgoing ? e.to_concept : e.from_concept;
    const reference = outgoing ? e.to : e.from;
    const row = el('div', 'edge' + (e.exists ? '' : ' missing'));
    row.append(el('span', 'badge', e.kind + (outgoing ? ' → ' : ' ← ')));
    if (concepts.has(target)) {
      const button = el('button', '', reference); button.addEventListener('click', () => navigate(target)); row.append(button);
    } else row.append(el('span', '', reference + ' (missing)'));
    return row;
  };
  const render = () => {
    let id = '';
    try { id = decodeURIComponent(location.hash.slice(1)); } catch (_) { id = ''; }
    // A valid concept route wins over Goldmark's footnote anchor namespace.
    if (!concepts.has(id) && /^fn(?:ref\d*)?:/i.test(id) && Array.from(byId.querySelectorAll('[id]')).some(anchor => anchor.id === id)) return;
    const c = concepts.get(id) || data.concepts[0];
    byId.replaceChildren();
    if (!c) { byId.append(el('p', 'empty', 'This bundle contains no concepts.')); return; }
    byId.append(el('h2', '', c.title), el('code', '', c.id));
    const meta = el('div', 'meta');
    [
      [c.type || 'type unknown', false],
      ['trust: ' + c.trust, false],
      ['status: ' + c.status, c.status === 'deprecated' || c.status === 'invalid'],
      ['staleness: ' + c.staleness, c.staleness === 'stale' || c.staleness === 'invalid']
    ].forEach(([value, alert]) => meta.append(el('span', 'badge' + (alert ? ' stale' : ''), value)));
    byId.append(meta);
    if (c.stale_after) byId.append(el('p', '', 'Stale after: ' + c.stale_after));
    const body = el('div', 'body'); body.innerHTML = c.html; byId.append(body);
    // Bundle resolution comes from Go. Relative Markdown links use concept deep links.
    const markdownLinks = data.edges.filter(e => e.kind === 'markdown' && e.from_concept === c.id);
    body.querySelectorAll('a').forEach(a => {
      const raw = a.getAttribute('href') || '';
      const normalized = value => { try { return decodeURI(value); } catch (_) { return value; } };
      const edge = markdownLinks.find(e => e.raw === raw || normalized(e.raw) === normalized(raw));
      if (edge && edge.exists && concepts.has(edge.to_concept)) {
        a.href = '#' + encodeURIComponent(edge.to_concept);
      } else if (edge) {
        a.removeAttribute('href'); a.title = 'Missing concept';
      } else if (/^#fn(?:ref\d*)?:/i.test(raw)) {
        // Goldmark footnotes link to anchors in the current detail card.
        a.addEventListener('click', event => {
          const target = document.getElementById(raw.slice(1));
          if (target) { event.preventDefault(); target.scrollIntoView(); }
        });
      } else if (/^(https?:\/\/|mailto:)/i.test(raw) && !/[\u0000-\u001f\u007f]/.test(raw)) {
        a.rel = 'noopener noreferrer'; a.referrerPolicy = 'no-referrer';
      } else {
        a.removeAttribute('href'); a.title = 'Unresolved link';
      }
    });
    byId.append(el('h3', '', 'Sources'));
    if (!c.sources.length) byId.append(el('p', 'empty', 'No sources recorded.'));
    c.sources.forEach(s => {
      const row = el('div', 'source', (s.title || s.id || 'Source') + (s.resource ? ' · ' : ''));
      if (/^(https?:\/\/|mailto:)/i.test(s.resource) && !/[\u0000-\u001f\u007f]/.test(s.resource)) {
        const a = el('a', '', s.resource); a.href = s.resource; a.rel = 'noopener noreferrer'; a.referrerPolicy = 'no-referrer'; row.append(a);
      } else row.append(el('span', '', s.resource));
      byId.append(row);
    });
    const outgoing = data.edges.filter(e => e.from_concept === c.id);
    const incoming = data.edges.filter(e => e.to_concept === c.id);
    byId.append(el('h3', '', 'Outgoing')); if (!outgoing.length) byId.append(el('p', 'empty', 'None.')); outgoing.forEach(e => byId.append(edgeRow(e, true)));
    byId.append(el('h3', '', 'Incoming')); if (!incoming.length) byId.append(el('p', 'empty', 'None.')); incoming.forEach(e => byId.append(edgeRow(e, false)));
    if (showGraph.checked) {
      const panel = el('div', 'graph'); panel.append(el('strong', '', 'Local graph · ' + (outgoing.length + incoming.length) + ' connections'));
      const nearby = [...outgoing, ...incoming].slice(0, 200);
      const ids = [...new Set(nearby.map(e => e.from_concept === c.id ? e.to_concept : e.from_concept))].slice(0, 48);
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.setAttribute('viewBox', '0 0 600 360'); svg.setAttribute('role', 'img');
      svg.setAttribute('aria-label', 'Local relationship diagram for ' + c.id);
      const svgEl = (tag, attrs) => {
        const node = document.createElementNS('http://www.w3.org/2000/svg', tag);
        for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
        return node;
      };
      const center = svgEl('circle', {cx:300,cy:180,r:27,fill:'#155da0'});
      svg.append(center);
      const centerText = svgEl('text', {x:300,y:185,'text-anchor':'middle',fill:'#fff','font-size':11});
      centerText.textContent = 'current'; svg.append(centerText);
      ids.forEach((id, index) => {
        const angle = index * Math.PI * 2 / ids.length - Math.PI / 2;
        const x = 300 + 245 * Math.cos(angle), y = 180 + 135 * Math.sin(angle);
        svg.append(svgEl('line', {x1:300,y1:180,x2:x,y2:y,stroke:'#8294aa','stroke-width':1.5}));
        const point = svgEl('circle', {cx:x,cy:y,r:14,fill:concepts.has(id)?'#55a3c8':'#b56f6f'});
        if (concepts.has(id)) {point.style.cursor='pointer'; point.addEventListener('click', () => navigate(id));}
        svg.append(point);
        const label = svgEl('text', {x:x,y:y-19,'text-anchor':'middle','font-size':10,fill:'#172335'});
        label.textContent = id.length > 25 ? id.slice(0,22) + '…' : id; svg.append(label);
      });
      if (ids.length) panel.append(svg);
      nearby.forEach(e => panel.append(edgeRow(e, e.from_concept === c.id)));
      if (new Set(nearby.map(e => e.from_concept === c.id ? e.to_concept : e.from_concept)).size > 48) panel.append(el('p', 'empty', 'Diagram shows first 48 neighbors; the list below shows up to 200.'));
      if (outgoing.length + incoming.length > 200) panel.append(el('p', 'empty', 'Showing first 200 connections; use the edge lists above for all.'));
      byId.append(el('h3', '', 'Graph'), panel);
    }
    list.querySelectorAll('button').forEach(b => b.classList.toggle('active', b.dataset.id === c.id));
  };
  const updateList = () => {
    const q = search.value.toLocaleLowerCase(); const t = type.value;
    list.replaceChildren();
    let count = 0;
    for (const c of data.concepts) {
      if (t && c.type !== t) continue;
      if (q && !(c.id + ' ' + c.title + ' ' + c.type + ' ' + c.search_text).toLocaleLowerCase().includes(q)) continue;
      count++;
      const button = el('button', '', c.title); button.dataset.id = c.id;
      button.append(el('small', '', c.id + (c.type ? ' · ' + c.type : '')));
      button.addEventListener('click', () => navigate(c.id)); list.append(button);
    }
    if (!count) list.append(el('p', 'empty', 'No matching concepts.'));
    render();
  };
  search.addEventListener('input', updateList); type.addEventListener('change', updateList);
  showGraph.addEventListener('change', render); addEventListener('hashchange', render);
  updateList();
})();
