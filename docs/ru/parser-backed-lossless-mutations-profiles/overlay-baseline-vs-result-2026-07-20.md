---
title: "Изменения с сохранением оформления: сравнение исходного и итогового наложения"
description: "Исторический отчёт о проверках и измерениях производительности."
lang: ru
permalink: /ru/parser-backed-lossless-mutations-profiles/overlay-baseline-vs-result-2026-07-20/
documentation_id: docs-parser-backed-lossless-mutations-profiles-overlay-baseline-vs-result-2026-07-20
status: historical
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
---

{% include nav_ru.html %}

> Исторический материал. Измерения и статус относятся к дате исходного отчёта. Перевод сверён с [английским оригиналом редакции `61e75e9`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/docs/parser-backed-lossless-mutations-profiles/overlay-baseline-vs-result-2026-07-20.md) и не является новой редакцией отчёта.

# Изменения с сохранением оформления: сравнение исходного и итогового наложения {#parser-backed-lossless-mutations-overlay-baseline-vs-result}

Измерения записаны 2026-07-20 на `darwin/arm64`, Apple M1 Max, Go 1.26.5. Обе стороны используют `BenchmarkParserBackedOverlayComparison`, одинаковые примеры с 10,000 файлами, подготовленное содержимое размером 256 байт, одну версию Go, оборудование, `-benchtime=3x` и `-count=3`. Таблица показывает медиану `ns/op`; столбцы выделений памяти содержат стабильные значения всех трёх измерений.

Исходное состояние — коммит `43f7214` (`mutable`), до реализации изменений на основе синтаксических деревьев. Сравнительный тест производительности автономен; его без изменений скопировали в архив этого коммита:

```sh
mkdir /private/tmp/okf-parser-backed-baseline
git archive 43f7214 | tar -x -C /private/tmp/okf-parser-backed-baseline
cp mutation/overlay_comparison_benchmark_test.go \
  /private/tmp/okf-parser-backed-baseline/mutation/overlay_comparison_benchmark_test.go

(cd /private/tmp/okf-parser-backed-baseline && \
  GOCACHE=/private/tmp/okf-go-cache-baseline \
  go test ./mutation -run '^$' \
    -bench '^BenchmarkParserBackedOverlayComparison$' \
    -benchtime=3x -benchmem -count=3)

GOCACHE=/private/tmp/okf-go-cache-result \
go test ./mutation -run '^$' \
  -bench '^BenchmarkParserBackedOverlayComparison$' \
  -benchtime=3x -benchmem -count=3
```

| Пример | Исходное ns/op | Итоговое ns/op | Исходное B/op | Итоговое B/op | Исходное allocs/op | Итоговое allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| клонирование изменённого наложения, 10,000 | 1,379,528 | 489,667 | 3,347,232 | 1,049,456 | 10,036 | 37 |
| повторный вызов Paths, базовое состояние, 10,000 | 3,001,708 | 1,467,111 | 764,581 | 600,704 | 35 | 34 |
| переименование подготовленного файла, 10,000 | 1,253,625 | 467,014 | 3,349,077 | 1,049,701 | 10,044 | 38 |

Сокращение выделений памяти при клонировании и переименовании подготовленного файла — ожидаемое следствие совместного использования закрытых неизменяемых байтов содержимого при сохранении защитного копирования в общедоступном чтении. Результат повторного вызова Paths включает копирование общедоступного результата с обеих сторон. Итоговое дерево повторно использует кэш успешно перечисленных и нормализованных базовых путей.
