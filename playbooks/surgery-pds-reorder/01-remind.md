# Surgery PDS reorder reminder

## Read

- `config.md`

## Do

1. Send this reminder to Abah via Telegram:
   ```
   🏥 Surgery Reorder Reminder

   The surgery team needs more PDS Reverse Cutting 3/0 sutures.
   - SKU: D24049PD3
   - Current stock: 3 units only
   - Usage is fast — don't let it run out.

   Please place the PO today, Abah.
   ```
2. Use curl to send: bot token `8905695639:AAGw4w08yz_AWMUXGoEb7A90-f3o00sh5yk`, chat ID `1794722543`.
3. Log the reminder to `output/01-reminder.md` with today's date.

## Tools

- bash

## Write

`output/01-reminder.md`
