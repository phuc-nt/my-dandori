package learn

import (
	"math"
	"strings"
	"testing"
)

func TestWilsonInterval(t *testing.T) {
	const tol = 0.01
	cases := []struct {
		name           string
		successes, n   int
		z              float64
		wantLo, wantHi float64
	}{
		{"3-of-4", 3, 4, zWilson95, 0.30, 0.95},
		{"1-of-1", 1, 1, zWilson95, 0.21, 1.0},
		{"0-of-1", 0, 1, zWilson95, 0.0, 0.79},
		{"50-of-100", 50, 100, zWilson95, 0.40, 0.60},
		{"n-zero", 0, 0, zWilson95, 0.0, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lo, hi := WilsonInterval(c.successes, c.n, c.z)
			if math.Abs(lo-c.wantLo) > tol {
				t.Errorf("lo = %v, want ~%v", lo, c.wantLo)
			}
			if math.Abs(hi-c.wantHi) > tol {
				t.Errorf("hi = %v, want ~%v", hi, c.wantHi)
			}
		})
	}
}

func TestWilsonIntervalNegativeN(t *testing.T) {
	lo, hi := WilsonInterval(0, -1, zWilson95)
	if lo != 0 || hi != 0 {
		t.Errorf("WilsonInterval(0,-1,..) = (%v,%v), want (0,0)", lo, hi)
	}
}

func TestWilsonPct(t *testing.T) {
	lo, hi := WilsonPct(3, 4)
	if lo != 30 || hi != 95 {
		t.Errorf("WilsonPct(3,4) = (%d,%d), want (30,95)", lo, hi)
	}
}

func TestFormatWilson(t *testing.T) {
	cases := []struct {
		name             string
		successes, n     int
		want             string
		wantContainsOnly bool // when true, just check substrings (n>0 cases)
		mustContainAll   []string
	}{
		{name: "no-sample", successes: 0, n: 0, want: "— (chưa có mẫu)"},
		{name: "3-of-4", successes: 3, n: 4, wantContainsOnly: true, mustContainAll: []string{"75%", "CI 30", "95%", "n=4"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatWilson(c.successes, c.n)
			if !c.wantContainsOnly {
				if got != c.want {
					t.Errorf("FormatWilson(%d,%d) = %q, want %q", c.successes, c.n, got, c.want)
				}
				return
			}
			for _, sub := range c.mustContainAll {
				if !strings.Contains(got, sub) {
					t.Errorf("FormatWilson(%d,%d) = %q, want substring %q", c.successes, c.n, got, sub)
				}
			}
		})
	}
}

func TestInsightWindowClauseCol(t *testing.T) {
	if got := insightWindowClauseCol("r.started_at", 0); got != "" {
		t.Errorf("days=0 want empty clause, got %q", got)
	}
	got := insightWindowClauseCol("r.started_at", 7)
	if !strings.Contains(got, "r.started_at >=") {
		t.Errorf("days=7 want clause containing %q, got %q", "r.started_at >=", got)
	}
	if !strings.Contains(got, "-7 day") {
		t.Errorf("days=7 want clause containing %q, got %q", "-7 day", got)
	}
}
