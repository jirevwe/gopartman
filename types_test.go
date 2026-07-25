package gopartman

import (
	"testing"
	"time"
)

func TestPartitionIntervalLabel(t *testing.T) {
	cases := []struct {
		name string
		want string
		in   time.Duration
		bad  bool
	}{
		{name: "hourly", in: PartitionHourInterval, want: "hourly"},
		{name: "daily", in: PartitionDayInterval, want: "daily"},
		{name: "weekly", in: PartitionWeekInterval, want: "weekly"},
		{name: "monthly sentinel", in: PartitionMonthInterval, want: "monthly"},
		{name: "random", in: 17 * time.Minute, bad: true},
		{name: "zero", in: 0, bad: true},
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
