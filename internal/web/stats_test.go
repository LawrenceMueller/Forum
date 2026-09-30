package web

import "testing"

func TestCommas(t *testing.T) {
	cases := map[int64]string{
		0:        "0",
		7:        "7",
		999:      "999",
		1000:     "1,000",
		12408:    "12,408",
		1234567:  "1,234,567",
		-1234567: "-1,234,567",
	}
	for n, want := range cases {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}
