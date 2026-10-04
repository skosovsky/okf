---
type: Decision
title: Worker leases
---

# Worker leases

## Lease ownership
A worker claims a job with a lease token and renews the lease every ten seconds. Finishing a job requires the current token. An expired worker cannot acknowledge a job after another worker acquires ownership.

## Владение задачей
Воркер получает задачу с токеном аренды и продлевает аренду каждые десять секунд. Завершение требует текущий токен. Старый воркер не может подтвердить задачу после смены владельца.
