package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MrShanks/networth/internal/money"
	"github.com/MrShanks/networth/internal/store"
)

type workspaceAccount struct {
	store.Account
	Months []workspaceBalance
}

type workspaceBalance struct {
	Amount money.Amount
	Month  string
	Known  bool
}

type workspaceMonth struct {
	Value string
	Label string
}

type workspaceData struct {
	Accounts        []workspaceAccount
	Currencies      []string
	AssetClasses    []string
	Month           string
	Months          []workspaceMonth
	Totals          []money.Amount
	Chart           Chart
	CurrentNetWorth money.Amount
	Liquidity       money.Amount
	Invested        money.Amount
	Illiquidity     money.Amount
	CurrentAsOf     string
	Base            string
	Year            int
	CurrentYear     int
	Error           string
	Notice          string
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ledger := loaded.ledger
	now := time.Now()
	completedMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)
	year := completedMonth.Year()
	if selected := strings.TrimSpace(r.URL.Query().Get("year")); selected != "" {
		parsed, parseErr := strconv.Atoi(selected)
		if parseErr != nil || parsed < 1900 || parsed > completedMonth.Year() {
			s.redirectTo(w, r, "/", "year must be between 1900 and the current year")
			return
		}
		year = parsed
	}
	data := workspaceData{
		Currencies:   store.Currencies,
		AssetClasses: []string{store.ClassCash, store.ClassStocks, store.ClassBonds, store.ClassStocksBonds, store.ClassRealEstate},
		Month:        completedMonth.Format("2006-01"),
		Base:         store.Base,
		Year:         year,
		CurrentYear:  completedMonth.Year(),
		Error:        r.URL.Query().Get("err"),
		Notice:       r.URL.Query().Get("msg"),
	}
	current := ledger.AccountBalancesAt(completedMonth.Format("2006-01") + "-31")
	data.CurrentNetWorth = current.NetWorth()
	for _, class := range current.Allocation().Classes {
		switch class.Class {
		case store.ClassCash:
			data.Liquidity += class.Total
		case store.ClassStocks, store.ClassBonds, store.ClassStocksBonds:
			data.Invested += class.Total
		case store.ClassRealEstate:
			data.Illiquidity += class.Total
		}
	}
	data.CurrentAsOf = completedMonth.Format("January 2006")
	lastMonth := 12
	if year == completedMonth.Year() {
		lastMonth = int(completedMonth.Month())
	}
	for month := 1; month <= lastMonth; month++ {
		value := fmt.Sprintf("%04d-%02d", year, month)
		data.Months = append(data.Months, workspaceMonth{Value: value, Label: time.Month(month).String()[:3]})
	}
	for _, account := range ledger.Accounts {
		row := workspaceAccount{Account: account}
		points := ledger.Balances(account.ID)
		for _, month := range data.Months {
			monthEnd := month.Value + "-31"
			balance := workspaceBalance{Month: month.Value}
			for _, point := range points {
				if point.AsOf <= monthEnd {
					balance.Amount = point.Amount
					balance.Known = true
				}
			}
			row.Months = append(row.Months, balance)
		}
		data.Accounts = append(data.Accounts, row)
	}
	for _, month := range data.Months {
		valuation := ledger.AccountBalancesAt(month.Value + "-31")
		total := valuation.NetWorth()
		data.Totals = append(data.Totals, total)
	}
	data.Chart = buildWorkspaceNetWorthChart(ledger, completedMonth)
	s.render(w, "workspace.html", data)
}

func buildWorkspaceNetWorthChart(ledger *store.Ledger, completedMonth time.Time) Chart {
	var first string
	for _, account := range ledger.Accounts {
		for _, point := range ledger.Balances(account.ID) {
			month := point.AsOf[:7]
			if first == "" || month < first {
				first = month
			}
		}
	}
	if first == "" {
		return Chart{Empty: true}
	}
	start, _ := time.Parse("2006-01", first)
	end := time.Date(completedMonth.Year(), completedMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	var points []datedValue
	for month := start; !month.After(end); month = month.AddDate(0, 1, 0) {
		key := month.Format("2006-01")
		total := ledger.AccountBalancesAt(key + "-31").NetWorth()
		points = append(points, datedValue{
			date: month.Format("Jan 2006"), value: total.Float(), label: total.String() + " " + store.Base,
		})
	}
	return plot(points)
}

func (s *Server) handleCreateWorkspaceAccount(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.redirectTo(w, r, "/", "account name is required")
		return
	}
	amount, err := money.Parse(r.FormValue("balance"))
	if err != nil {
		s.redirectTo(w, r, "/", "balance must look like 1234.56")
		return
	}
	if err := s.store.CreateAccount(r.Context(), name, "", "", r.FormValue("kind"),
		r.FormValue("currency"), store.ClassCash, r.FormValue("month"), &amount); err != nil {
		s.redirectTo(w, r, "/", "could not add account: "+err.Error())
		return
	}
	s.noticeTo(w, r, "/", "Account added.")
}

func (s *Server) handleSetWorkspaceAccountDetails(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/", "invalid account")
		return
	}
	if err := s.store.SetAccountDetails(r.Context(), accountID,
		r.FormValue("name"), r.FormValue("currency"), r.FormValue("asset_class")); err != nil {
		s.redirectTo(w, r, "/", "could not update account: "+err.Error())
		return
	}
	s.noticeTo(w, r, "/", "Account updated.")
}

func (s *Server) handleDeleteWorkspaceAccount(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/", "invalid account")
		return
	}
	if err := s.store.DeleteAccount(r.Context(), accountID); err != nil {
		s.redirectTo(w, r, "/", "could not delete account: "+err.Error())
		return
	}
	s.noticeTo(w, r, "/", "Account deleted.")
}

func (s *Server) handleSetWorkspaceBalance(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	if rawID == "" {
		rawID = r.FormValue("account_id")
	}
	accountID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		s.redirectTo(w, r, "/", "invalid account")
		return
	}
	amount, err := money.Parse(r.FormValue("balance"))
	if err != nil {
		s.redirectTo(w, r, "/", "balance must look like 1234.56")
		return
	}
	if err := s.store.SetBalance(r.Context(), accountID, r.FormValue("month"), amount); err != nil {
		s.redirectTo(w, r, "/", "could not save balance: "+err.Error())
		return
	}
	s.noticeTo(w, r, "/", "Monthly balance saved.")
}

func (s *Server) handleImportWorkspaceBalances(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		s.redirectTo(w, r, "/", "could not read the upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.redirectTo(w, r, "/", "choose a CSV file to import")
		return
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		s.redirectTo(w, r, "/", "could not parse CSV: "+err.Error())
		return
	}
	var snapshots []store.BalanceSnapshot
	for index, row := range rows {
		if len(row) != 3 && len(row) != 5 {
			s.redirectTo(w, r, "/", fmt.Sprintf("row %d must contain account, month, balance, and optionally currency and type", index+1))
			return
		}
		if index == 0 && strings.EqualFold(strings.TrimSpace(row[0]), "account") {
			continue
		}
		amount, err := money.Parse(row[2])
		if err != nil {
			s.redirectTo(w, r, "/", fmt.Sprintf("row %d has an invalid balance", index+1))
			return
		}
		snapshot := store.BalanceSnapshot{
			AccountName: strings.TrimSpace(row[0]), Month: strings.TrimSpace(row[1]), Amount: amount,
		}
		if len(row) == 5 {
			snapshot.Currency = strings.ToUpper(strings.TrimSpace(row[3]))
			snapshot.Kind = strings.ToLower(strings.TrimSpace(row[4]))
		}
		snapshots = append(snapshots, snapshot)
	}
	if len(snapshots) == 0 {
		s.redirectTo(w, r, "/", "the CSV contains no balance rows")
		return
	}
	created, err := s.store.ImportBalances(r.Context(), snapshots)
	if err != nil {
		s.redirectTo(w, r, "/", "could not import balances: "+err.Error())
		return
	}
	message := fmt.Sprintf("Imported %d monthly balance snapshots.", len(snapshots))
	if created > 0 {
		message = fmt.Sprintf("Imported %d monthly balance snapshots and created %d account%s.",
			len(snapshots), created, plural(created, "", "s"))
	}
	s.noticeTo(w, r, "/", message)
}
