---
layout: default
title: "Набор проверки просмотрщика для Linux и macOS"
lang: ru
permalink: /ru/readings/viewer/testdata/regression-016/README/
document_id: viewer-testdata-regression-016-readme
source: viewer/testdata/regression-016/README.txt
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
preserve_source: true
---

{% include nav_ru.html %}

# Набор проверки просмотрщика для Linux и macOS {#page-top}

Это полный перевод инструкции [viewer/testdata/regression-016/README.txt](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/viewer/testdata/regression-016/README.txt), сверенный с ревизией `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. Исходные файлы набора не изменяются. Инструкция создаёт отдельный набор документов для проверки регрессий в браузере; каталог шаблонов напрямую выгружать не следует.

## Создать набор и проверить результат {#materialize}

Шаблоны `.md.fixture` позволяют клонировать репозиторий в Windows. Они сохраняют точные байты исходного содержимого файлов `fn:example.md`, `fnref:example.md` и `fnref1:example.md`.

Тест `TestRouteFixturePreservesFootnotesAndUnusualIDs` создаёт файлы с этими именами в новом каталоге с помощью существующей вспомогательной функции, затем загружает настоящий набор документов и проверяет те же маршруты, связи, происхождение данных и разметку сносок Goldmark. В Windows тест пропускается, поскольку такие имена файлов с двоеточиями недопустимы. Проверка маршрутизации рабочего JavaScript выполняется независимо, когда доступен Node.

Для ручной выгрузки и проверки регрессий в браузере сначала создайте набор в Linux или macOS. Выполните команды из корня репозитория; Python нужен только для этой ручной инструкции:

```sh
fixture_dir=$(mktemp -d)
python3 - "$fixture_dir" <<'PYTHON'
from pathlib import Path
import sys
source = Path('viewer/testdata/regression-016')
target = Path(sys.argv[1])
for path in source.iterdir():
    name = path.name
    if name.endswith('.md.fixture'):
        name = name.removesuffix('.fixture').replace('-example', ':example', 1)
    elif not name.endswith('.md'):
        continue
    (target / name).write_bytes(path.read_bytes())
PYTHON
go run ./cmd/okf view "$fixture_dir" --output "$fixture_dir/viewer.html"
```

Откройте полученный `viewer.html`. Если запустить `view` непосредственно для каталога шаблонов, заметки с двоеточиями в идентификаторах не появятся.
