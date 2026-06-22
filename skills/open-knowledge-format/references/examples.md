# Примеры OKF bundles

Три полных conformant bundle из разных доменов.

---

## 1. E-commerce analytics

```text
ecommerce/
├── index.md
├── tables/
│   ├── index.md
│   ├── orders.md
│   └── customers.md
└── metrics/
    ├── index.md
    └── gross-revenue.md
```

### tables/orders.md

```markdown
---
type: BigQuery Table
title: Заказы
description: Одна строка на завершенный customer order по всем каналам.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders
tags: [sales, orders, revenue]
timestamp: 2026-05-28T14:30:00Z
schema:
  fields:
    - id: col-customer_id
      name: customer_id
      relations:
        joins_to:
          - target: tables/customers#col-customer_id
---

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `order_id` | STRING | Глобально уникальный идентификатор заказа |
| `customer_id` | STRING | FK на [customers](./customers.md) |
| `total_usd` | NUMERIC | Сумма заказа в долларах США |
| `placed_at` | TIMESTAMP | Когда customer отправил заказ |
| `channel` | STRING | Канал привлечения: web, mobile, pos |

# Joins

- Join with [customers](./customers.md) по `customer_id`
- Используется metric [gross revenue](/metrics/gross-revenue.md)

# Citations

[1] [BigQuery schema docs](https://cloud.google.com/bigquery/docs/schemas)
```

### tables/customers.md

```markdown
---
type: BigQuery Table
title: Customers
description: Одна строка на зарегистрированного customer с profile и lifetime data.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=customers
tags: [sales, customers]
timestamp: 2026-05-28T14:30:00Z
---

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `customer_id` | STRING | Primary key |
| `email` | STRING | Email customer, hashed в production |
| `created_at` | TIMESTAMP | Дата регистрации |
| `ltv_usd` | NUMERIC | Lifetime value в USD |

# Joins

- Используется [orders](./orders.md) по `customer_id`
```

### metrics/gross-revenue.md

````markdown
---
type: Metric
title: Gross Revenue
description: Общая выручка до refunds и discounts.
tags: [revenue, finance, kpi]
timestamp: 2026-05-28T14:30:00Z
relations:
  depends_on:
    - target: tables/orders#col-total_usd
---

# Definition

Сумма `total_usd` из [orders](/tables/orders.md) за период.
Refunds не вычитаются; для этого нужна metric Net Revenue.

# SQL

```sql
SELECT DATE_TRUNC(placed_at, MONTH) as month,
       SUM(total_usd) as gross_revenue
FROM `acme.sales.orders`
GROUP BY 1
```

# Related

- Source table: [orders](/tables/orders.md)
- Связанная metric: Net Revenue, то есть gross minus refunds
````

### index.md (root)

```markdown
# E-commerce Analytics Bundle

- [Tables](./tables/) - Database tables для analytics stack
- [Metrics](./metrics/) - Business KPIs, рассчитанные из tables
```

---

## 2. SaaS incident playbooks

```text
incidents/
├── index.md
├── alerts/
│   ├── index.md
│   ├── api-latency-p99.md
│   └── db-connections.md
└── runbooks/
    ├── index.md
    └── escalate-incident.md
```

### alerts/api-latency-p99.md

````markdown
---
type: Alert
title: API Latency P99 > 2s
description: Срабатывает, когда 99-й перцентиль API latency выше 2 секунд в течение 5 минут.
tags: [api, latency, critical]
severity: critical
timestamp: 2026-06-01T09:00:00Z
---

# Trigger Condition

```promql
histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m])) > 2
```

# Impact

Пользователи получают timeouts. Downstream services могут упасть каскадом.

# Response

1. Проверить [DB connections alert](./db-connections.md) - это часто root cause.
2. Перейти к [escalation runbook](/runbooks/escalate-incident.md), если проблема не решена за 10 минут.
3. Проверить deployment log на недавние changes.

# Citations

[1] [SLA definition](https://wiki.internal/sla/api-latency)
````

### runbooks/escalate-incident.md

```markdown
---
type: Runbook
title: Escalate Incident
description: Шаги escalation, если on-call не может решить incident в пределах SLA.
tags: [oncall, incident, escalation]
timestamp: 2026-06-01T09:00:00Z
---

# When to Escalate

- Alert не resolved за 10 минут.
- Customer-facing impact подтвержден.
- Несколько alerts срабатывают одновременно.

# Steps

1. Написать в Slack channel #incidents со ссылкой на alert.
2. Вызвать secondary on-call через PagerDuty.
3. Если P1: вызвать Engineering Manager.
4. Создать incident document из template.
5. Обновить status page, если impact customer-facing.

# Contacts

| Role | Who | Method |
|------|-----|--------|
| Secondary on-call | Rotation | PagerDuty |
| Eng Manager | @manager | Slack DM |
| Infra lead | @infra-lead | Slack DM |
```

---

## 3. API documentation

```text
api/
├── index.md
├── auth/
│   ├── index.md
│   └── oauth2-flow.md
├── endpoints/
│   ├── index.md
│   └── create-order.md
└── policies/
    ├── index.md
    └── rate-limits.md
```

### endpoints/create-order.md

````markdown
---
type: API Endpoint
title: Create Order
description: Создает новый order для authenticated customer.
resource: https://api.acme.com/v2/orders
tags: [orders, write, v2]
method: POST
timestamp: 2026-05-20T10:00:00Z
---

# POST /v2/orders

Создает новый order. Требует [OAuth2 authentication](/auth/oauth2-flow.md).

# Request

```json
{
  "customer_id": "cust_abc123",
  "items": [{"sku": "WIDGET-01", "quantity": 2}],
  "idempotency_key": "unique-request-id"
}
```

# Response (201 Created)

```json
{
  "order_id": "ord_xyz789",
  "status": "pending",
  "total_usd": 49.98,
  "created_at": "2026-05-20T10:30:00Z"
}
```

# Errors

| Code | Meaning |
|------|---------|
| 400 | Некорректный request body |
| 401 | Missing or invalid auth token |
| 409 | Duplicate `idempotency_key` |
| 429 | Превышен [rate limit](/policies/rate-limits.md) |

# Rate Limits

Подчиняется [rate limiting](/policies/rate-limits.md). См. headers `X-RateLimit-*`.
````

### policies/rate-limits.md

```markdown
---
type: Policy
title: Rate Limits
description: Rate limits по тарифным планам для всех API endpoints.
tags: [policy, rate-limit, api]
timestamp: 2026-05-20T10:00:00Z
---

# Limits by Plan

| Plan | Requests/min | Burst |
|------|--------------|-------|
| Free | 60 | 10 |
| Pro | 600 | 100 |
| Enterprise | 6000 | 1000 |

# Response Headers

Каждый response включает:

- `X-RateLimit-Limit`: max requests per window
- `X-RateLimit-Remaining`: оставшиеся requests в window
- `X-RateLimit-Reset`: Unix timestamp сброса window

# When Exceeded

Возвращает `429 Too Many Requests`. Повторять после `X-RateLimit-Reset`.
Применяется ко всем endpoints, включая [create order](/endpoints/create-order.md).
```
