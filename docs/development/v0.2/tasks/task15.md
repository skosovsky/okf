# Техническое задание: transactional migration OKF v0.1 → v0.2

## 1. Цель

Добавить детерминированный preview/apply migration plan для двух breaking
changes v0.2:

- `timestamp` → `generated`;
- `# Citations` → `sources` + keyed footnotes.

Зависимости: [`task10.md`](task10.md), [`task11.md`](task11.md),
[`task13.md`](task13.md), [`task14.md`](task14.md), [`task17.md`](task17.md).

## 2. Общий contract

- Preview read-only по умолчанию.
- Apply требует exact base revision и plan digest.
- Весь bundle меняется одной transaction; partial migration запрещена.
- Unknown frontmatter/body/assets сохраняются.
- Root `okf_version` меняется последним после target-v0.2 validation.
- Повторный identical run — noop/idempotent replay.

## 3. Timestamp migration

Создавать `generated` только с explicit `generated.by`.
`ChangeSet.Actor` не использовать как неявный producer.

Cases:

- generated absent + timestamp present → copy to `generated.at`;
- both absent → нужны explicit values, no `time.Now()`;
- equivalent generated exists → noop;
- conflict `timestamp`/`generated.at` → blocker без explicit policy;
- legacy removal policy: `preserve` или `remove_after_copy`;
- duplicate/merge/comment-ambiguous nodes → fail closed.

## 4. Citation migration

Нельзя угадывать provenance или claim attribution по смыслу.

Input должен содержать explicit legacy entry → stable source ID mapping.

Поддержать:

- `[1] [Title](URL)`;
- upstream bullet URL form;
- explicit ID/title/resource;
- replacement только доказанных numeric markers;
- creation keyed footnote definitions;
- removal `# Citations` только после полного successful mapping.

Fail closed:

- multiple Citations sections;
- duplicate numbers/labels;
- unresolved entry/claim marker;
- conflicting footnote definition;
- ambiguous Markdown ownership.

Не генерировать title/author/usage/last_modified/verified/status/stale_after.

## 5. Markdown collector

Добавить parser-backed ownership для:

- section spans;
- citation entries;
- footnote references/definitions;
- normalized label collisions;
- `# Computation` heading/fence.

Fenced/inline code и raw HTML остаются opaque. Existing MoveConcept parser
configuration не менять глобально.

## 6. Attested Computation migration boundary

Автоматически не дробить narrative document и не превращать prose SQL в
Attested Computation.

Разрешить только explicit creation/move plan:

- caller задаёт target concepts и sanctioned computation;
- inline mode владеет одним fence под одним heading;
- file mode создаёт referenced asset через transactional store support;
- multiple headings/fences → Ambiguous.

## 7. Tests

Все tests — AAA.

- Canonical Appendix v0.1 → v0.2.
- Dry-run leaves bytes unchanged.
- Timestamp policies/conflicts/actor validation.
- Citation URL/path/explicit mapping.
- Multiple sections, duplicate IDs, code/raw HTML markers.
- CRLF/LF, comments, flow YAML.
- One bad file yields zero writes.
- Revision conflict/plan mismatch/concurrent writer.
- Crash/recovery leaves pre- or post-state only.
- Second migration is noop.
- Source/body/unknown extension byte-preservation.
- Target v0.2 validation passes.

## 8. Acceptance criteria

- Safe cases migrate transactionally and losslessly.
- Ambiguous cases return blockers/manual actions.
- No inferred verification/trust/freshness/actor.
- Root version never changes before full preflight.
- CLI/MCP use the same planner.
- Race, recovery и full suite зелёные.

## 9. Out of scope

LLM rewrite, semantic claim matching, automatic SQL splitting, executor runtime,
attestation ABI и network fetching.
