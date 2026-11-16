package transport

import "testing"

func TestCanonicalNumericUID(t *testing.T) {
	for _, id := range []string{"0", "999", "1000", "9007199254740993", "9223372036854775807"} {
		if !ValidUserID(id) {
			t.Fatalf("valid UID rejected: %s", id)
		}
	}
	for _, id := range []string{"", "user_old", "01", "+1000", "-1", "1000.0", "1e3", " 1000 ", "１０００", "9223372036854775808"} {
		if ValidUserID(id) {
			t.Fatalf("non-canonical UID accepted: %s", id)
		}
	}
}
