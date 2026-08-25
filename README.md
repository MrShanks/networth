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

## Liquidity and asset allocation

Every account and fund has an **asset class** — Cash, Stocks, Bonds or Other —
shown as a pill you can change right on the dashboard, next to the currency
pill. It defaults to Cash for accounts and Stocks for funds, which is right
for a checking account or an ETF, but not for everything: a pension held
entirely in equities, for example, is still just a cash balance in the app, so
mark that account Stocks and its balance is treated as invested rather than
liquid.

The **Liquidity** panels in Summary split net worth into what is sitting in a
plain cash account and what is invested, using that classification. The
**Asset allocation** widget breaks the same total down by class, so
reclassifying an account or a fund updates the dashboard immediately.

## Currencies

Everything is reported in **CHF**. Accounts and funds can be held in CHF, EUR or
USD, and non-CHF amounts are converted at the current exchange rate — including
the historical points on the chart, so the whole curve is expressed in today's
CHF. An account's currency pill on the dashboard is a dropdown: pick a different
currency to relabel the account, for example if it was created wrong — this
only changes how the stored balance is read, it does not convert the number.

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
