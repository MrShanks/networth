// Package money handles amounts as integer minor units (cents) to avoid float rounding.
package money

import (
	"errors"
	"strconv"
	"strings"
)

type Amount int64

var ErrInvalid = errors.New("invalid amount")

// Parse accepts values like "1234.56", "1,234.56", "-20" and returns cents.
func Parse(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, ErrInvalid
	}

	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}

	intPart, fracPart, _ := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 2 {
		return 0, ErrInvalid
	}
	fracPart += strings.Repeat("0", 2-len(fracPart))

	units, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	frac, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}

	total := units*100 + frac
	if neg {
		total = -total
	}
	return Amount(total), nil
}

// String renders the amount with a thousands separator, e.g. "-1,234.56".
func (a Amount) String() string {
	n := int64(a)
	neg := n < 0
	if neg {
		n = -n
	}

	units := strconv.FormatInt(n/100, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range units {
		if i > 0 && (len(units)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	b.WriteByte('.')
	b.WriteString(strconv.FormatInt(n%100/10, 10))
	b.WriteString(strconv.FormatInt(n%10, 10))
	return b.String()
}

// Input renders a plain value suitable for an HTML number input.
func (a Amount) Input() string {
	return strconv.FormatFloat(float64(a)/100, 'f', 2, 64)
}

func (a Amount) Float() float64 { return float64(a) / 100 }
