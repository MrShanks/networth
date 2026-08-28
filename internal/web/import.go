package web

import (
	"net/http"

	"github.com/MrShanks/networth/internal/importer"
	"github.com/MrShanks/networth/internal/store"
)

// maxUpload caps how much of an upload is read into memory.
const maxUpload = 8 << 20

type importData struct {
	Base      string
	Files     int
	Added     int
	Duplicate int
	Skips     []importer.Skip
	Rows      int
	Error     string
}

func (s *Server) handleImportExpenses(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		s.redirectTo(w, r, "/transactions", "could not read the upload: "+err.Error())
		return
	}

	headers := r.MultipartForm.File["file"]
	if len(headers) == 0 {
		s.redirectTo(w, r, "/transactions", "choose a CSV file to import")
		return
	}

	var expenses []store.Expense
	var skips []importer.Skip
	rows := 0
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			s.redirectTo(w, r, "/transactions", header.Filename+": "+err.Error())
			return
		}
		result, parseErr := importer.Parse(file, importer.Options{Currencies: store.Currencies})
		file.Close()
		if parseErr != nil {
			s.redirectTo(w, r, "/transactions", header.Filename+": "+parseErr.Error())
			return
		}
		rows += len(result.Rows)
		skips = append(skips, result.Skips...)
		for _, row := range result.Rows {
			kind := store.KindExpense
			if row.Income {
				kind = store.KindIncome
			}
			if row.Transfer {
				kind = store.KindTransfer
			}
			expenses = append(expenses, store.Expense{
				Kind: kind, AsOf: row.Date, AccountRef: row.AccountRef,
				Category: row.Category, Note: row.Note, Currency: row.Currency, Amount: row.Amount,
			})
		}
	}

	rules, err := s.store.Rules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	added, duplicates, err := s.store.ImportExpenses(r.Context(), store.Categorise(expenses, rules))
	if err != nil {
		s.fail(w, err)
		return
	}

	s.render(w, "import.html", importData{
		Base:      store.Base,
		Files:     len(headers),
		Added:     added,
		Duplicate: duplicates,
		Skips:     skips,
		Rows:      rows,
	})
}
