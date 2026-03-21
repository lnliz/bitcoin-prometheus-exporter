package main

import (
	"testing"
)

func TestHashpsNameDesc(t *testing.T) {
	cases := []struct {
		in       int
		wantName string
		wantDesc string
	}{
		{-1, "bitcoin_hashps_neg1", "Estimated network hash rate per second since the last difficulty change"},
		{1, "bitcoin_hashps_1", "Estimated network hash rate per second for the last 1 blocks"},
		{120, "bitcoin_hashps_120", "Estimated network hash rate per second for the last 120 blocks"},
	}

	for _, tc := range cases {
		name, desc := hashpsNameDesc(tc.in)
		if name != tc.wantName || desc != tc.wantDesc {
			t.Fatalf("hashpsNameDesc(%d) = (%q, %q), want (%q, %q)", tc.in, name, desc, tc.wantName, tc.wantDesc)
		}
	}
}
