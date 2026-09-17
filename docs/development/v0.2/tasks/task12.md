# Техническое задание: graph projection profile для OKF v0.2

## 1. Цель

Расширить `graph` проекцией provenance, trust, lifecycle и Attested Computation,
не выдавая project-specific RDF/JSON-LD vocabulary за часть upstream OKF.

Зависимости: [`task09.md`](task09.md), [`task10.md`](task10.md).

## 2. Contract boundary

Upstream SPEC не определяет JSON-LD context, RDF ontology или graph ABI.
Текущий `https://okf.io/ontology/v0.1#` — контракт toolkit, а не нормативная
часть OKF.

До изменения namespace создать ADR и versioned projection contract:

- legacy profile сохраняет byte-compatible v0.1 output;
- новый profile явно маркируется как `skosovsky/okf` projection v0.2;
- declared/effective OKF version и graph profile version — разные поля;
- нельзя молча переименовать existing predicates.

## 3. API

Сохранить существующие `Render*` wrappers для compatibility и добавить
options/profile API:

- effective spec version;
- projection profile;
- optional explicit `as_of` для derived staleness;
- extension relation policy.

`graph` получает typed data из `bundle`; raw YAML повторно не парсит.

## 4. Projection

JSON-LD и N-Triples должны уметь выразить:

- generated actor/time и legacy fallback provenance marker;
- normalized verification events;
- derived trust tier;
- effective status, stale date и staleness evaluated at explicit date;
- sources, credibility signals и shared/per-source usage window;
- keyed claim attribution;
- Attested Computation runtime, parameters, computation/executor/attester
  resources и declared receipt fields;
- local referenced assets как отдельные resource nodes, когда path resolvable;
- declared/effective OKF version и compatibility mode.

Unknown runtime/parameter types сохраняются как literals. Unknown frontmatter
не переводить в invented predicates; при необходимости давать отдельный
lossless raw/frontmatter extension payload только в documented profile.

## 5. Edges и extensions

- Markdown links остаются untyped `references`.
- `sources[].resource`, computation/executor/attester paths получают только
  явно project-profile predicates.
- Scope descriptors и external URLs не считаются bundle nodes.
- Broken local paths остаются видимыми с `exists=false`.
- YAML `relations` сохраняется как отдельно маркированный toolkit extension.
- `sources[].id` не становится subresource fragment.
- Graph renderer ничего не исполняет и не читает сеть.

Text/DOT/Mermaid могут сохранить topology-only output; metadata annotations
добавлять только через explicit option, чтобы не сломать snapshots.

## 6. Determinism

- Stable order concepts, sources, verifications, parameters, receipts и edges.
- Bare/list verified дают одинаковую projection.
- Empty collections кодируются одинаково согласно profile.
- Dates/timestamps получают корректные RDF datatypes только после valid typed
  parsing.
- Writer/short-write errors пробрасываются всеми renderers.

## 7. Файлы

- `graph/jsonld.go`;
- `graph/ntriples.go`;
- `graph/text.go`, `dot.go`, `mermaid.go` только для options wiring;
- новые `profile.go`, `projection.go`;
- package tests и CLI/MCP contract fixtures;
- ADR/documentation projection profile.

## 8. Tests

Все tests — AAA.

- v0.1 legacy output byte-compatible.
- Full Appendix A v0.2 projection.
- All trust tiers и bare/list verified.
- Missing status/default stable и stale boundary.
- Shared/per-source usage windows.
- Unknown source IDs/runtime/types.
- Local/external/scope/broken resources.
- `sources[].id` не создаёт fragment.
- Extension relations явно маркированы.
- Deterministic output на shuffled input.
- JSON-LD parses; N-Triples escaping/datatype/IRI tests.
- Writer failure для каждого renderer.

## 9. Acceptance criteria

- v0.1 consumers могут выбрать legacy profile.
- v0.2 signal families доступны в documented JSON-LD/N-Triples profile.
- Namespace/profile не заявлен как upstream standard.
- CLI и MCP используют один graph implementation.
- No execution/network side effects.
- `go test ./graph` и `go test ./...` проходят.

## 10. Out of scope

Official ontology standardization, runtime execution/attestation, semantic score,
network dereference и inference новых relation types из prose.
