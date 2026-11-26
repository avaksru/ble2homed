package parser

import (
	"testing"

	"github.com/avaksru/ble2homed/pkg/types"
)

func TestIsITAGAdvertisement(t *testing.T) {
	cases := []struct {
		name      string
		advName   string
		itagNames []string
		want      bool
	}{
		{"exact iTAG", "iTAG", []string{"iTAG"}, true},
		{"case insensitive", "itag", []string{"iTAG"}, true},
		{"uppercase", "ITAG", []string{"iTAG"}, true},
		{"with spaces", " iTAG ", []string{"iTAG"}, true},
		{"other name", "LYWSD03MMC", []string{"iTAG"}, false},
		{"empty name", "", []string{"iTAG"}, false},
		{"empty list", "iTAG", []string{}, false},
		{"multiple names match", "KeyFinder", []string{"iTAG", "KeyFinder"}, true},
		{"multiple names no match", "Tracker", []string{"iTAG", "KeyFinder"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adv := types.Advertisement{Name: tc.advName}
			got := IsITAGAdvertisement(adv, tc.itagNames)
			if got != tc.want {
				t.Errorf("IsITAGAdvertisement(%q, %v) = %v, want %v", tc.advName, tc.itagNames, got, tc.want)
			}
		})
	}
}
