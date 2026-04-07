package main

import "testing"

func TestK6TokenPoolSize(t *testing.T) {
	tests := []struct {
		name string
		rps  int
		want int
	}{
		{name: "caps low values", rps: 0, want: 10},
		{name: "uses rps in range", rps: 60, want: 60},
		{name: "caps high values", rps: 300, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := k6TokenPoolSize(tt.rps); got != tt.want {
				t.Fatalf("k6TokenPoolSize(%d) = %d, want %d", tt.rps, got, tt.want)
			}
		})
	}
}
