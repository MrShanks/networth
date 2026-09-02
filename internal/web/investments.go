package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/importer"
	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/prices"
	"github.com/MrShanks/networth/internal/store"
)

type investmentsData struct {
	Accounts   []store.Account
	Positions  []store.Position
	Trades     []store.TradeActivity
	Growth     Chart
	Allocation []investmentAllocation
	Currencies []string
	Market     money.Amount
	Cost       money.Amount
	Gain       money.Amount
	Base       string
	Today      string
	Error      string
	Notice     string
}

type investmentAllocation struct {
	Name     string
	Ticker   string
	Value    money.Amount
	Share    float64
	BarWidth int
}

func (s *Server) handleInvestments(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	activities, err := s.store.TradeActivities(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	valuation := loaded.ledger.At(time.Now().Format("2006-01-02"))
	data := investmentsData{
		Accounts: loaded.ledger.Accounts, Positions: valuation.Positions(), Trades: activities,
		Growth:     buildInvestChart(loaded.ledger.InvestHistory()),
		Currencies: store.Currencies, Market: valuation.MarketBase, Cost: valuation.InvestedBase,
		Gain: valuation.GainBase, Base: store.Base, Today: time.Now().Format("2006-01-02"),
		Error: r.URL.Query().Get("err"), Notice: r.URL.Query().Get("msg"),
	}
	for _, position := range data.Positions {
		share := 0.0
		if data.Market > 0 {
			share = float64(position.ValueBase) / float64(data.Market) * 100
		}
		data.Allocation = append(data.Allocation, investmentAllocation{
			Name: position.Name, Ticker: position.Ticker, Value: position.ValueBase,
			Share: share, BarWidth: int(math.Round(share)),
		})
	}
	sort.Slice(data.Allocation, func(i, j int) bool { return data.Allocation[i].Value > data.Allocation[j].Value })
	s.render(w, "investments.html", data)
}

func (s *Server) handleImportInvestments(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/investments", "choose an investment account")
		return
	}
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		s.redirectTo(w, r, "/investments", "could not read the upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.redirectTo(w, r, "/investments", "choose a Transactions.csv file")
		return
	}
	defer file.Close()

	result, err := importer.ParseTrades(file, importer.Options{Currencies: store.Currencies})
	if err != nil {
		s.redirectTo(w, r, "/investments", "could not parse trades: "+err.Error())
		return
	}
	trades := make([]store.BrokerTrade, 0, len(result.Trades))
	for _, trade := range result.Trades {
		trades = append(trades, store.BrokerTrade{
			Name: trade.Product, Ticker: trade.ISIN, Currency: trade.Currency,
			AsOf: trade.Date, Units: trade.Units, Price: trade.Price,
		})
	}
	added, duplicates, err := s.store.ImportTrades(r.Context(), accountID, trades)
	if err != nil {
		s.redirectTo(w, r, "/investments", "could not import trades: "+err.Error())
		return
	}
	message := fmt.Sprintf("Imported %d trade%s; %d duplicate%s skipped", added, plural(added, "", "s"), duplicates, plural(duplicates, "", "s"))
	if len(result.Skips) > 0 {
		message += fmt.Sprintf("; %d invalid row%s skipped", len(result.Skips), plural(len(result.Skips), "", "s"))
	}
	refreshed, failed := s.refreshInvestmentPrices(r.Context())
	message += fmt.Sprintf("; refreshed %d fund price%s", refreshed, plural(refreshed, "", "s"))
	if failed > 0 {
		message += fmt.Sprintf("; %d price refresh%s failed", failed, plural(failed, "", "es"))
	}
	s.noticeTo(w, r, "/investments", message+".")
}

func (s *Server) handleAddInvestmentTrade(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/investments", "choose an investment account")
		return
	}
	units, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("units")), 64)
	if err != nil || units <= 0 || math.IsNaN(units) || math.IsInf(units, 0) {
		s.redirectTo(w, r, "/investments", "units must be a positive number")
		return
	}
	price, err := money.Parse(r.FormValue("price"))
	if err != nil || price < 0 {
		s.redirectTo(w, r, "/investments", "price must look like 123.45")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	ticker := strings.ToUpper(strings.TrimSpace(r.FormValue("ticker")))
	if name == "" && ticker == "" {
		s.redirectTo(w, r, "/investments", "fund name or ISIN is required")
		return
	}
	if name == "" {
		name = ticker
	}
	added, duplicates, err := s.store.ImportTrades(r.Context(), accountID, []store.BrokerTrade{{
		Name: name, Ticker: ticker, Currency: r.FormValue("currency"),
		AsOf: r.FormValue("as_of"), Units: units, Price: price,
	}})
	if err != nil {
		s.redirectTo(w, r, "/investments", "could not add purchase: "+err.Error())
		return
	}
	if duplicates > 0 || added == 0 {
		s.noticeTo(w, r, "/investments", "That purchase is already recorded.")
		return
	}
	refreshed, failed := s.refreshInvestmentPrices(r.Context())
	message := "Purchase added."
	if refreshed > 0 {
		message = "Purchase added and current price refreshed."
	} else if failed > 0 {
		message = "Purchase added, but the current price could not be refreshed."
	}
	s.noticeTo(w, r, "/investments", message)
}

func (s *Server) handleRefreshInvestmentPrices(w http.ResponseWriter, r *http.Request) {
	refreshed, failed := s.refreshInvestmentPrices(r.Context())
	if refreshed == 0 && failed > 0 {
		s.redirectTo(w, r, "/investments", "could not refresh current prices")
		return
	}
	message := fmt.Sprintf("Refreshed current prices for %d fund%s", refreshed, plural(refreshed, "", "s"))
	if failed > 0 {
		message += fmt.Sprintf("; %d failed", failed)
	}
	s.noticeTo(w, r, "/investments", message+".")
}

func (s *Server) refreshInvestmentPrices(ctx context.Context) (refreshed, failed int) {
	ledger, err := s.store.Load(ctx)
	if err != nil {
		return 0, 1
	}
	for _, fund := range ledger.Funds {
		symbol := fund.Symbol
		if symbol == "" {
			query := fund.Ticker
			if query == "" {
				query = fund.Name
			}
			symbol, err = s.prices.Symbol(ctx, query, fund.Currency)
			if err != nil {
				if !errors.Is(err, prices.ErrNoListing) {
					s.log.Error("resolve investment symbol", "fund", fund.Name, "error", err)
				}
				failed++
				continue
			}
		}
		from, err := s.store.FirstTradeDate(ctx, fund.ID)
		if err != nil || from == "" {
			failed++
			continue
		}
		history, currency, err := s.prices.History(ctx, symbol, from)
		if err != nil || !strings.EqualFold(currency, fund.Currency) || len(history) == 0 {
			failed++
			continue
		}
		points := make([]store.PricePoint, 0, len(history))
		for _, point := range history {
			points = append(points, store.PricePoint{AsOf: point.AsOf, Price: point.Close})
		}
		if _, err := s.store.ImportPrices(ctx, fund.ID, points); err != nil {
			failed++
			continue
		}
		if fund.Symbol == "" {
			if err := s.store.SetFundSymbol(ctx, fund.ID, symbol); err != nil {
				failed++
				continue
			}
		}
		refreshed++
	}
	return refreshed, failed
}
