package gopartman

import (
	"testing"
	"time"
)

func TestPartitionIntervalLabel(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
		bad  bool
	}{
		{"hourly", PartitionHourInterval, "hourly", false},
		{"daily", PartitionDayInterval, "daily", false},
		{"weekly", PartitionWeekInterval, "weekly", false},
		{"monthly sentinel", PartitionMonthInterval, "monthly", false},
		{"random", 17 * time.Minute, "", true},
		{"zero", 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PartitionIntervalLabel(tc.in)
			if tc.bad {
				if err == nil {
					t.Errorf("want error for %s, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
