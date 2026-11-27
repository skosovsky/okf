# T06. Сохранять метаданные при каждой записи

bundle/index.go: preserveRootIndexVersion собирает новый frontmatter только с okf_version. setup/setup.go и internal/okfcli/init.go содержат литерал 0.2. В mutation и store/fs уже есть lossless patches, preview/apply и журнал.

## Результат

- Писать root title, description, license (SPDX expression либо путь), okf_version и любые расширения. При регенерации сохранить весь исходный frontmatter даже без версии; при неизменных метаданных сохранить его исходные байты. Для fmt документировать намеренную нормализацию.
- Покрыть unknown keys на каждом уровне, не только x-: элемент sources/verified/parameters, generated, executor, attester, root index. Патч одного поля сохраняет соседние; ошибки выбора элемента не допускают потери данных.
- Применить контракт ко всем писателям: Go, fmt -w, index, init, setup, write_concept, patch, миграция, backfill. Обновление содержания не выдаёт проверку автоматически; предусмотреть явную передачу времени и автора, отличать редактирование текста от технической правки.

## Приёмка

- [ ] У root index с license и вложенным расширением два последовательных index дают тот же frontmatter; отдельно проверить полное отсутствие okf_version.
- [ ] Для каждого публичного writer зафиксирован round-trip с чужими ключами. Неизменённые документы байт-в-байт равны; fmt проверен на сохранение значений. Затронутые ID, revision и receipt проверены.
- [ ] Новый init/setup создаёт 0.3 по согласованному default. Повторный apply, конфликт конкурентной правки и ошибка между файлами сохраняют гарантии store.

[Общий goal и зависимости](GOAL.md)
