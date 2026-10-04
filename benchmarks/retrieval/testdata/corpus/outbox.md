---
type: Decision
title: Outbox decision
---

# Outbox decision

## Atomic delivery
Orders and the outbox record are written in one database transaction. The dispatcher publishes a pending record to Kafka after commit. A crash after publication can cause duplicate delivery; consumers use the event identifier as an idempotency key.

## Решение об отправке
Заказ и запись outbox сохраняются в одной транзакции базы. Диспетчер отправляет запись в Kafka после commit. Повторная доставка допустима: потребитель проверяет идентификатор события.
