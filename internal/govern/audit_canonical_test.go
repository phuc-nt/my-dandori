package govern

import "testing"

// TestCanonicalHashResistsDelimiterShift proves the fix for the old
// "|"-joined chainHash: two different field tuples that concatenate to the
// SAME "|"-joined string (moving a "|" byte from one field's content into
// the delimiter position of an adjacent field) collided under the old hash
// but must NOT collide under the length-prefixed canonical encoding.
func TestCanonicalHashResistsDelimiterShift(t *testing.T) {
	// Old scheme: prev+"|"+ts+"|"+actor+"|"+action+"|"+subject+"|"+detail.
	// tupleA's subject ends with "|x" and detail is "y"; tupleB's subject is
	// "" and detail is "x|y" — both concatenate (with "|" joins) to the
	// identical byte string "...|subject|x|y" vs "...||x|y"... construct an
	// exact collision directly:
	//
	//   A: action="act", subject="foo|bar", detail="baz"
	//   B: action="act", subject="foo",     detail="bar|baz"
	//
	// Old join: prev|ts|actor|act|foo|bar|baz  (A)
	//           prev|ts|actor|act|foo|bar|baz  (B)
	// Identical strings — the "|" moved from being inside subject (A) to
	// being the delimiter itself (B), an actual collision in the old scheme.
	prev, ts, actor, action := "genesis", "2026-01-01T00:00:00Z", "tester", "act"

	oldJoinA := prev + "|" + ts + "|" + actor + "|" + action + "|" + "foo|bar" + "|" + "baz"
	oldJoinB := prev + "|" + ts + "|" + actor + "|" + action + "|" + "foo" + "|" + "bar|baz"
	if oldJoinA != oldJoinB {
		t.Fatalf("test setup invalid: old-scheme joins must collide to prove the fix; got %q vs %q", oldJoinA, oldJoinB)
	}

	hashA := canonicalHash(prev, ts, actor, action, "foo|bar", "baz")
	hashB := canonicalHash(prev, ts, actor, action, "foo", "bar|baz")
	if hashA == hashB {
		t.Errorf("canonicalHash collided on delimiter-shifted fields (%s) — length-prefix encoding failed to disambiguate boundaries", hashA)
	}
}

// TestCanonicalHashDeterministic sanity-checks the same inputs always
// produce the same hash (a hash function that isn't deterministic would
// break the whole chain model).
func TestCanonicalHashDeterministic(t *testing.T) {
	h1 := canonicalHash("genesis", "ts", "actor", "action", "subject", "detail")
	h2 := canonicalHash("genesis", "ts", "actor", "action", "subject", "detail")
	if h1 != h2 {
		t.Fatalf("canonicalHash not deterministic: %s != %s", h1, h2)
	}
	h3 := canonicalHash("genesis", "ts", "actor", "action", "subject", "different")
	if h1 == h3 {
		t.Errorf("canonicalHash did not change when detail changed")
	}
}
