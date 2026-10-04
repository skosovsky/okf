---
type: Decision
title: Cache invalidation
---

# Cache invalidation

## Revision keys
The catalog cache includes the product revision in each key. A changed product obtains a new revision rather than deleting every cached entry. Entries expire after ten minutes; payment authorization never uses cached prices.

## Ключи каталога
Кэш каталога использует ревизию товара в ключе. Изменение товара создаёт новую ревизию. Записи истекают через десять минут. Авторизация платежа не использует цену из кэша.
