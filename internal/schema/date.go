package schema

import (
	"fmt"
	"time"
)

// DateLayout is the only date format Gandalf reads or writes in frontmatter.
const DateLayout = "2006-01-02"

// Date is a calendar date with no time or timezone component. The zero Date
// means "unset".
type Date struct {
	t time.Time
}

// NewDate returns the calendar date on which t falls, in t's own location.
func NewDate(t time.Time) Date {
	y, m, d := t.Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// Today returns the current calendar date in the machine's local timezone.
func Today() Date { return NewDate(time.Now()) }

// ParseDate parses a YYYY-MM-DD date.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: want YYYY-MM-DD", s)
	}
	return Date{t}, nil
}

// IsZero reports whether the date is unset.
func (d Date) IsZero() bool { return d.t.IsZero() }

// String renders the date as YYYY-MM-DD, or the empty string when unset.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.t.Format(DateLayout)
}

// Before reports whether d falls strictly before other.
func (d Date) Before(other Date) bool { return d.t.Before(other.t) }

// Equal reports whether d and other are the same calendar date.
func (d Date) Equal(other Date) bool { return d.t.Equal(other.t) }

// Year returns the date's year.
func (d Date) Year() int { return d.t.Year() }

// Month returns the date's month.
func (d Date) Month() time.Month { return d.t.Month() }
