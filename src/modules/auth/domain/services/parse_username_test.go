package services

import "testing"

func TestParseUsername(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantAsset string
		wantOk    bool
	}{
		{"happy path", "asset-aaa", "asset-aaa", true},
		{"empty input", "", "", false},
		{"legacy colon shape rejected", "org-1:asset-aaa", "", false},
		{"trailing colon rejected", "asset-aaa:", "", false},
		{"leading colon rejected", ":asset-aaa", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAsset, gotOk := ParseUsername(tt.input)
			if gotOk != tt.wantOk || gotAsset != tt.wantAsset {
				t.Fatalf("ParseUsername(%q) = (%q,%t), want (%q,%t)",
					tt.input, gotAsset, gotOk, tt.wantAsset, tt.wantOk)
			}
		})
	}
}
