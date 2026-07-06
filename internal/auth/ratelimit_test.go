package auth

import "testing"

func TestRateLimiterAllowIP(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < bucketCapacity; i++ {
		if !rl.AllowIP("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed within bucket capacity", i+1)
		}
	}
	if rl.AllowIP("1.2.3.4") {
		t.Error("attempt beyond bucket capacity must be denied (429)")
	}
}

func TestRateLimiterAllowIPIndependentPerIP(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < bucketCapacity; i++ {
		rl.AllowIP("1.2.3.4")
	}
	if !rl.AllowIP("5.6.7.8") {
		t.Error("a different IP must have its own independent bucket")
	}
}

func TestRateLimiterAccountLockout(t *testing.T) {
	rl := NewRateLimiter()
	if !rl.AllowAccount("alice") {
		t.Fatal("account with no failures must be allowed")
	}
	for i := 0; i < maxAccountFails; i++ {
		rl.RecordFailure("alice")
	}
	if rl.AllowAccount("alice") {
		t.Error("account must be locked out after maxAccountFails failures, regardless of IP")
	}
}

func TestRateLimiterAccountLockoutIndependentPerAccount(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < maxAccountFails; i++ {
		rl.RecordFailure("alice")
	}
	if !rl.AllowAccount("bob") {
		t.Error("a different account must not be affected by alice's lockout")
	}
}

func TestRateLimiterResetAccount(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < maxAccountFails; i++ {
		rl.RecordFailure("alice")
	}
	if rl.AllowAccount("alice") {
		t.Fatal("precondition: alice should be locked out")
	}
	rl.ResetAccount("alice")
	if !rl.AllowAccount("alice") {
		t.Error("ResetAccount must clear the lockout (called after successful login)")
	}
}
