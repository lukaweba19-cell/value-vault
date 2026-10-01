package promos

import "testing"

// Titles come only from the hand-maintained knownTitles map. Unknown ids
// must return "" so the caller prints the raw resource (code or link) —
// never a guessed name.
func TestTitleMapOnly(t *testing.T) {
	cases := []struct {
		offerID string
		want    string
	}{
		{"6870AAC98E444CD69D948BD07D2D33B6", "3 Months Discord Nitro"},
		// lookup normalizes case and whitespace
		{" 6870aac98e444cd69d948bd07d2d33b6 ", "3 Months Discord Nitro"},
		// unknown ids must not be guessed (no Discord/hostname/Perk fallbacks)
		{"DEADBEEF000000000000000000000001", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Title(c.offerID); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.offerID, got, c.want)
		}
	}
}

// The line format: known ids get "Title [resource]", unknown ids print the
// resource exactly as-is (no guessed names).
func TestPromoLineFormat(t *testing.T) {
	if got := formatLine("E6D9E945214340C087B15373C0418E77", "REDEEM-CODE-1"); got != "Rainbow Six Siege IQ Verbena Bundle [REDEEM-CODE-1]" {
		t.Errorf("known id line = %q", got)
	}
	if got := formatLine("000000000000000000000000000000AA", "ABCD-1234-EFGH"); got != "ABCD-1234-EFGH" {
		t.Errorf("unknown id must print raw resource, got %q", got)
	}
	if got := formatLine("000000000000000000000000000000BB", "https://example.com/reward"); got != "https://example.com/reward" {
		t.Errorf("unknown link must print raw resource, got %q", got)
	}
}
