package money

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Amount{
		"0":         0,
		"12":        1200,
		"12.3":      1230,
		"12.34":     1234,
		"-5.05":     -505,
		"1,234.56":  123456,
		" 1234.56 ": 123456,
		".5":        50,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "abc", "1.234", "1.2.3"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected an error", in)
		}
	}
}

func TestString(t *testing.T) {
	cases := map[Amount]string{
		0:          "0.00",
		5:          "0.05",
		123456:     "1,234.56",
		-123456789: "-1,234,567.89",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Errorf("Amount(%d).String() = %q, want %q", int64(in), got, want)
		}
	}
}
