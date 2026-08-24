# networth

A small self-hosted web app for tracking your net worth over time.

You define **accounts** — a bank account, a broker such as Degiro, a mortgage —
and record what they are worth over time. An account holds a cash balance, any
number of **funds** (ETFs), or both. The dashboard values everything with the
latest data on or before today and plots the net worth history.

## Funds

Funds are tracked with two kinds of entry, both recorded from the same form:

- a **trade** — units bought or sold and the price you paid, which is what fixes
  the cost basis;
- a **price** — what a unit is worth on a date, to mark the holding to market.

Leave the units field empty to record a price on its own; a trade always sets
the price for its day as well.

Buying more of a fund you already own is just another trade: it raises the
amount invested rather than showing up as growth.

- **Invested** is the average cost of the units you still hold, net of anything
  sold.
- **Gain** is the current market value minus that, plus anything realized on a
  sale.

The dashboard charts market value against money paid in, so a contribution shows
as a step in the invested line while the gap between the two lines is growth.
Each fund keeps its figures in its own currency; only the totals are converted.

## Expenses

The **Expenses** page tracks monthly spending, separately from net worth so
nothing is counted twice. Each expense is an amount, a currency, a category, an
optional note and a date. The page shows the selected month's total, the
monthly average, a bar per month (click one to switch months) and a breakdown by
category. Amounts in EUR or USD are totalled in CHF at the current rate.

## Records

The **Records** page ranks your months: the ten best and ten worst for
investments, and the ten heaviest for spending. Investment months are scored on
market movement only — money you paid into a fund that month is subtracted, so
buying more never shows up as a good month.

## Retirement

The **Retirement** page projects how long you still need to work. Your monthly
spending is taken automatically from the last 12 recorded months, and the target
is a year of that spending divided by the withdrawal rate — 4% means 25x your
yearly spending.

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
the historical points on the chart, so the whole curve is expressed in today's
CHF.

Rates come from the [Frankfurter](https://frankfurter.dev) API (daily ECB
reference rates, no API key), are refetched at most every 15 minutes, and are
cached in the database so the app keeps working offline. The dashboard shows
USD/CHF, EUR/USD and CHF/EUR and refreshes them in place every minute.

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
internal/retire   retirement projection, pure arithmetic
internal/store    SQLite schema, writes, and the in-memory ledger that values
                  accounts, funds and history
internal/web      HTTP handlers, templates, server-rendered SVG charts
```

## Notes

There is no authentication, so keep it bound to `localhost` or put it behind a
proxy that handles auth before exposing it.
