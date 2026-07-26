# Fetch pending purchase orders POs

Query the procurement database for purchase orders. Fetch pending POs and draft orders. Get purchase data for analysis.

## Read
- config.md

## Do
1. Query purchase_orders for Pending Payment and Draft statuses from last week
2. Flag any Draft older than 7 days as stale procurement
3. Write purchase data results as a markdown table with PO ID, supplier, total, status, age

## Write
output/01-pending-orders.md
