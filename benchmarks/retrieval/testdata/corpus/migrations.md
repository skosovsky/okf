---
type: Decision
title: Database rollout
---

# Database rollout

## Expand and contract
Deploy an additive nullable column before new writers use it. Backfill existing rows in bounded batches. Remove the old column only after all readers switch to the replacement. A rollback keeps both columns until the release is confirmed.

## Изменение схемы
Сначала добавить nullable колонку, затем обновить код записи. Старые строки заполнить небольшими пакетами. Удалить старую колонку после переключения всех читателей. При откате обе колонки сохраняются.
