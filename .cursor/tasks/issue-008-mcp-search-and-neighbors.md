# 008 — Ограниченный поиск и соседние concepts через MCP

Статус: backlog. Приоритет: P1. Зависимости: временная семантика 004 при выдаче lifecycle; interoperability 005 при чтении reserved files.

## Проблема

В MCP есть `list_concepts`, `read_concept` и `get_semantic_graph`, но нет отдельного поиска и bounded neighborhood. Для одного вопроса агент вынужден читать лишние данные.

Reference: [read-only MCP](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/servers/okf_mcp.py). Их substring search и две группы neighbors подходят как стартовая модель, Python server переносить не нужно.

## Предлагаемый контракт — сначала Schema

- Два новых read-only tools в нашем Go server: `search_concepts` и `get_neighbors`. Имена и точные DTO утвердить перед реализацией.
- Search: `bundle_path`, непустой query, bounded limit; поиск по ID/title/description/tags/body. Metadata выше body, ties по canonical ConceptID. Определить Unicode/case normalization и смысл совпадения, не обещать semantic/embedding search.
- Neighbor: canonical concept ID, направление, bounded limit и явные kinds. Markdown navigation, provenance source links и наша `relations` extension не должны незаметно смешиваться.
- Согласовать, считать ли implicit relation двухшаговым через source node или concept-to-concept edge; fragment и escaped ID разрешать существующим parser/resolver.
- Выдавать маленькие concept cards, основания совпадения/edge kind, truncation и способ продолжения. Описать snapshot/revision semantics cursor или явно зафиксировать отсутствие pagination в v1.
- Trust/status/staleness выдаются раздельно через общий observation code. Без reference time не вычислять freshness через wall clock.
- JSON Schema input/output/error, resource caps и совместимость существующих девяти tools описать до кода.

## Реализация на Go

Общий query слой поверх bundle snapshot и существующих parser-backed links/projections. BYOT: не требовать фиксированной таксономии concepts или схемы пользовательского frontmatter. Избежать копирования Python regex parser и отдельного полного reload для каждого edge.

## Приёмка

- [ ] AAA-тесты ранжирования, пустого query, Unicode, limits, ties, отсутствующего ID и deterministic ordering.
- [ ] Neighbor fixtures: relative/root links, fragments, sources, extension relations, циклы, broken links, code-owned псевдоссылки.
- [ ] Traversal и symlink escape отклоняются существующими root boundaries.
- [ ] Cancellation/лимиты не возвращают success с молча неполным результатом.
- [ ] Contract tests проверяют tool schemas и совместимость старых tools.
- [ ] На generated Go fixture около 1000 concepts показано bounded output и измерено время запроса; не вводить search engine без необходимости.

## Не входит

Второй MCP server, embeddings, vector database, write tools, изменение upstream conformance.

## Решение о переносе

Их read-only MCP полезен там, где хост не читает файлы напрямую. Берём сценарии поиска и просмотра соседей; реализуем их в нашем Go server, чтобы не появились два источника правды о parsing, root boundaries и schema. Совпадающий `read_concept` уже есть.
