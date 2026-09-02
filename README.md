# networth

A small self-hosted personal-finance app.

The current interface focuses on transactions, expenses, graphs, records, and
retirement planning. The root route is an empty widget workspace. The previous
Overview and Portfolio interfaces were deliberately removed so Portfolio can be
rebuilt from a clean starting point.

## Financial model

The retained model supports asset and liability accounts, dated balances, and
funds held inside accounts. Funds have two kinds of entry:

- a **trade** — units bought or sold and the price you paid, which is what fixes
  the cost basis;
- a **price** — what a unit is worth on a date, to mark the holding to market.

Buying more of a fund you already own is just another trade: it raises the
amount invested rather than showing up as growth.

- **Invested** is the average cost of the units you still hold, net of anything
  sold.
- **Gain** is the current market value minus that, plus anything realized on a
  sale.

Each fund keeps its figures in its own currency; aggregate values are converted
to CHF. This model, the DEGIRO importer, and historical price fetching remain
available for the new Portfolio implementation, but currently have no
management UI.

## Expenses

The **Expenses** page tracks monthly cash flow, separately from net worth so
nothing is counted twice. Each entry is money spent or money earned, with an
amount, a currency, a category, an optional note and a date. A negative amount
on an expense is a refund, which nets off against the rest of the month.

The page shows the selected month's spending, income and what was left over,
with the saving as a share of what came in. Below that: the monthly average, a
bar per month (click one to switch months), a breakdown by category and every
entry. Amounts in EUR or USD are totalled in CHF at the current rate. A month's
entries can be cleared in one go from the entries panel.

A **Spending by category** table sits below it, covering every month at once:
what each category costs you per month on average, how many months it turns up
in, its total, its share of everything spent, and a sparkline of its trend.

### Importing from a bank

The page takes a CSV export straight from your bank. The account or card column
is ignored; the date, description, amount, currency and category are used. Swiss
and European number formats are both understood, as are comma and semicolon
separated files.

Only money going out is imported. Refunds — positive amounts on lines the bank
still calls an expense — come in as negative entries, so the month's total is
your real spending. Income lines are stored as income, which is what makes the
monthly saving figure possible. Left out, and listed with a reason after the
import:

- transfers — card invoice payments, moves between your own accounts and money
  sent to a broker — since those are neither spending nor earning and would
  double count. Tick the box on the form to keep them anyway;
- lines in a currency the app does not handle, or with an unreadable date.

Entries already stored are skipped, so importing the same export twice changes
nothing.

### Category rules

Banks categorise by merchant, which is often not how you think about the money.
A rule says *when the description contains X, put it in Y* — for example a
landlord's name into Rent. Rules run on every import and are also applied to
what is already stored the moment you save one, so a category can be fixed
everywhere at once. Matching ignores case, and the first matching rule wins.

## Records

The **Records** page ranks your months: the ten best and ten worst for
investments, and the ten heaviest for spending. Investment months are scored on
market movement only — money you paid into a fund that month is subtracted, so
buying more never shows up as a good month.

## Retirement

The **Retirement** page projects how long you still need to work. Your monthly
spending is taken automatically from the last 12 recorded months, and your
monthly savings from your income less your spending over the months where income
is known. The target is a year of that spending divided by the withdrawal rate —
4% means 25x your yearly spending.

Everything is projected in today's money: the expected return is entered before
inflation and the projection uses what is left after it, while savings are
assumed to keep pace with inflation. The chart plots the projection against the
target; where they meet is your date. Savings, return, inflation and withdrawal
rate can all be changed on the page.

A second chart answers the opposite question — how long the money would last if
you drew it down — under three scenarios:

- **Stop working now**, living off the portfolio from today;
- **Work one more year**, saving as you do now before stopping;
- **Passion project**, stopping now but still earning a little each month.

The last two are configurable: how many more months you work, and what the
passion project brings in.

## Currencies

Everything is reported in **CHF**. Accounts and funds can be held in CHF, EUR or
USD, and non-CHF amounts are converted at the current exchange rate — including
historical aggregate points.

Rates come from the [Frankfurter](https://frankfurter.dev) API (daily ECB
reference rates, no API key), are refetched at most every 15 minutes, and are
cached in the database so the app keeps working offline.

## Run

```sh
go run ./cmd/networth
```

Then open http://localhost:8080.

| Flag    | Default          | Description             |
| ------- | ---------------- | ----------------------- |
| `-addr` | `localhost:8080` | Address to listen on    |
| `-db`   | `networth.db`    | Path to the SQLite file |

Data lives in a single SQLite file, created on first start. Templates and CSS
are embedded in the binary, so `go build ./cmd/networth` produces a standalone
executable.

## Layout

```
cmd/networth      entry point, flags and graceful shutdown
internal/money    amounts stored as integer cents, parsing and formatting
internal/fx       exchange rate client with caching and offline fallback
internal/importer bank CSV parsing
internal/retire   retirement projection, pure arithmetic
internal/store    SQLite schema, writes, and the in-memory ledger that values
                  accounts, funds and history
internal/web      HTTP handlers, templates, server-rendered SVG charts
```

## Notes

There is no authentication, so keep it bound to `localhost` or put it behind a
proxy that handles auth before exposing it. Writes are rejected when the browser
reports them as cross-site, so another page cannot drive your local instance.
