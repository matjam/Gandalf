package schema

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "iso date", in: "2026-08-17", want: "2026-08-17"},
		{name: "leap day", in: "2024-02-29", want: "2024-02-29"},
		{name: "impossible day", in: "2026-02-30", wantErr: true},
		{name: "slashes", in: "2026/08/17", wantErr: true},
		{name: "with time", in: "2026-08-17T10:00:00Z", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDate(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDate(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseDate(%q).String() = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDateZeroValue(t *testing.T) {
	var d Date
	if !d.IsZero() {
		t.Error("zero Date reports set")
	}
	if d.String() != "" {
		t.Errorf("zero Date renders %q, want empty", d)
	}
}

func TestNewDateDropsTime(t *testing.T) {
	ts := time.Date(2026, time.August, 17, 23, 45, 12, 0, time.FixedZone("test", 3*60*60))
	if got := NewDate(ts).String(); got != "2026-08-17" {
		t.Errorf("NewDate(%v) = %q, want 2026-08-17", ts, got)
	}
}

func TestDateComparison(t *testing.T) {
	earlier, _ := ParseDate("2026-08-16")
	later, _ := ParseDate("2026-08-17")

	if !earlier.Before(later) {
		t.Error("earlier date does not report Before later")
	}
	if later.Before(earlier) {
		t.Error("later date reports Before earlier")
	}
	if !later.Equal(later) {
		t.Error("date does not equal itself")
	}
	if later.Year() != 2026 || later.Month() != time.August {
		t.Errorf("Year/Month = %d/%v, want 2026/August", later.Year(), later.Month())
	}
}
