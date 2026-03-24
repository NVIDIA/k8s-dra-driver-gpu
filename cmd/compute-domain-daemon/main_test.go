package main

import "testing"

func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, i := range items {
		m[i] = struct{}{}
	}
	return m
}

func TestShouldSendSIGUSR1(t *testing.T) {
	tests := []struct {
		name    string
		old     map[string]struct{}
		new     map[string]struct{}
		updated bool
		fresh   bool
		want    bool
	}{
		{
			name:    "no change, not updated",
			old:     set("A", "B", "C"),
			new:     set("A", "B", "C"),
			updated: false,
			fresh:   false,
			want:    false,
		},
		{
			name:    "no change, updated",
			old:     set("A", "B", "C"),
			new:     set("A", "B", "C"),
			updated: true,
			fresh:   false,
			want:    false,
		},
		{
			name:    "pure removal",
			old:     set("A", "B", "C"),
			new:     set("A", "B"),
			updated: true,
			fresh:   false,
			want:    false,
		},
		{
			name:    "addition",
			old:     set("A", "B"),
			new:     set("A", "B", "C"),
			updated: true,
			fresh:   false,
			want:    true,
		},
		{
			name:    "replacement same size",
			old:     set("A", "B", "C"),
			new:     set("A", "B", "D"),
			updated: true,
			fresh:   false,
			want:    true,
		},
		{
			name:    "remove and add",
			old:     set("A", "B", "C"),
			new:     set("A", "D"),
			updated: true,
			fresh:   false,
			want:    true,
		},
		{
			name:    "fresh process",
			old:     set("A", "B"),
			new:     set("A", "B", "C"),
			updated: true,
			fresh:   true,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSendSIGUSR1(tt.old, tt.new, tt.updated, tt.fresh)
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
