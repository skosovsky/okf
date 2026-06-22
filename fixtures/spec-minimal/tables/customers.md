---
type: BigQuery Table
title: Customers
description: One row per customer.
resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=customers
tags: [sales, customers]
timestamp: 2026-05-28T00:00:00Z
---

<!-- Synthetic non-verbatim completion scaffold: Appendix A lists this file but does not include its body. -->

# Schema

| Column | Type | Description |
|--------|------|-------------|
| `customer_id` | STRING | Unique customer identifier. |

Referenced by [orders](/tables/orders.md).
