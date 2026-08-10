package server

import "testing"

func TestDefaultKeepalivePolicy(t *testing.T) {
	p := DefaultKeepalivePolicy()
	if p.MinSeconds != 30 {
		t.Errorf("MinSeconds = %d, want 30", p.MinSeconds)
	}
	if p.MaxSeconds != 600 {
		t.Errorf("MaxSeconds = %d, want 600", p.MaxSeconds)
	}
	if p.DefaultSeconds != 60 {
		t.Errorf("DefaultSeconds = %d, want 60", p.DefaultSeconds)
	}
	if p.AllowDisable {
		t.Error("AllowDisable = true, want false")
	}
}

func TestKeepalivePolicy_Decide(t *testing.T) {
	p := DefaultKeepalivePolicy()
	tests := []struct {
		in   int64
		want int64
	}{
		{0, 60},     // proposal disabled and disallow disable -> default
		{-1, 60},    // negative proposal -> default
		{10, 30},    // below min -> min
		{1000, 600}, // above max -> max
		{60, 60},    // in range
		{300, 300},
		{30, 30},
		{600, 600},
	}
	for _, tt := range tests {
		if got := p.Decide(tt.in); got != tt.want {
			t.Errorf("Decide(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}

	disabled := &KeepalivePolicy{MinSeconds: 30, MaxSeconds: 600, DefaultSeconds: 60, AllowDisable: true}
	if got := disabled.Decide(0); got != 0 {
		t.Errorf("Decide(0) with AllowDisable = %d, want 0", got)
	}
}
