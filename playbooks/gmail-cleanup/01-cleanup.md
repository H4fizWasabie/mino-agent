# Daily Gmail cleanup

## Read

- `config.md`

## Do

### Phase 1: Auto-delete promos

1. Search for promotional emails older than 30 days: `older_than:30d category:promotions`
2. Delete exactly 20 of them using the email-deletion loop pattern (one per tool call).
3. NEVER touch supplier emails, PO emails, starred, or important emails. Promos and newsletters only.

### Phase 2: Recommend other bulk

4. Search for additional bulk-deletable emails (rotate daily):
   - Mon: `older_than:30d category:social`
   - Tue: `older_than:60d category:updates`
   - Wed: `older_than:30d category:forums`
   - Thu: older newsletters and notifications
   - Fri: catch up on remaining bulk from the week
5. For these, do NOT delete. Just list them as recommendations.

### Phase 3: Report

6. Send one Telegram report to Abah:
   ```
   🧹 Lunch Cleanup — [Date]

   🗑️ Auto-deleted: X promo emails
   
   📋 Recommended for review:
   - sender | subject | date | why safe
   - ...

   📊 Progress: ~X emails cleared so far
   ```
7. Bot: `8905695639:AAGw4w08yz_AWMUXGoEb7A90-f3o00sh5yk`, chat: `1794722543`

## Tools

- MCP_google_search_emails
- MCP_google_delete_email
- bash
- write_file

## Write

`output/01-cleanup-report.md`
