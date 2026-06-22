---
type: BigQuery Table
title: Orders
description: One row per completed customer order.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders
tags: [sales, orders]
timestamp: 2026-05-28T00:00:00Z
---

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `order_id` | STRING | Unique order identifier. |
| `customer_id` | STRING | FK to [customers](/tables/customers.md). |

Part of the [sales dataset](/datasets/sales.md).
