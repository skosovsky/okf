---
type: Decision
title: Retry policy
---

# Retry policy

## Retry budget
Transient HTTP failures use exponential backoff with jitter. The client retries at most three attempts inside the request deadline. Authentication failures are never retried. The shared retry budget protects the downstream service from overload.

## Повтор запросов
Временные HTTP ошибки допускают повтор с экспоненциальной задержкой и случайным разбросом. Максимум три попытки внутри deadline. Ошибки авторизации не повторяются; общий бюджет ограничивает нагрузку.
