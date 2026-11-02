package web

import (
	"testing"
	"time"
)

func TestRemaining(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name  string
		since time.Time
		done  float64
		want  float64
	}{
		{"not begun", time.Time{}, 0.5, 0},
		{"too early", now.Add(-3 * time.Second), 0.5, 0},
		{"too little done", now.Add(-time.Minute), 0.01, 0},
		{"done", now.Add(-time.Minute), 1, 0},
		{"a quarter in a minute", now.Add(-time.Minute), 0.25, 180},
		{"half in ten seconds", now.Add(-10 * time.Second), 0.5, 10},
	} {
		if got := remaining(c.since, c.done); got != c.want {
			t.Errorf("%s: remaining = %v, want %v", c.name, got, c.want)
		}
	}
}
