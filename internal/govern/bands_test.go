package govern

import (
	"context"
	"encoding/json"
	"testing"
)

func editCall(runID, path string) ToolCall {
	input, _ := json.Marshal(map[string]string{"file_path": path})
	return ExtractToolCall(runID, "a1", "proj", "/work/proj", "Write", input)
}

func TestBandSemantics(t *testing.T) {
	e := testEngine(t) // GateWaitSeconds=0 → gate hits become timeout denies
	seedRun(t, e, "b1", 0)
	ctx := context.Background()

	// Default (gated): in-cwd edit allowed, `git push` gated (deny on timeout).
	if d := e.Evaluate(ctx, editCall("b1", "/work/proj/x.go")); d.Verdict != Allow {
		t.Fatalf("gated: edit → %s (%s)", d.Verdict, d.Reason)
	}
	if d := e.Evaluate(ctx, bashCall("b1", "git push origin main")); d.Verdict != Deny {
		t.Fatalf("gated: push must gate → %s", d.Verdict)
	}

	// Supervised: even an in-cwd edit needs approval.
	if err := SetBand(e.St, "a1", BandSupervised, "tester", "test"); err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(ctx, editCall("b1", "/work/proj/x.go"))
	if d.Verdict != Deny {
		t.Fatalf("supervised: edit must gate → %s (%s)", d.Verdict, d.Reason)
	}
	var pendings int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE status='pending'`).Scan(&pendings)
	if pendings == 0 {
		t.Error("supervised edit must create an approval")
	}

	// Trusted: non-critical gate (git push) skipped; critical (terraform) still gated.
	if err := SetBand(e.St, "a1", BandTrusted, "tester", "test"); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(ctx, bashCall("b1", "git push origin main")); d.Verdict != Allow {
		t.Errorf("trusted: push must skip gate → %s (%s)", d.Verdict, d.Reason)
	}
	if d := e.Evaluate(ctx, bashCall("b1", "terraform apply -auto-approve")); d.Verdict != Deny {
		t.Errorf("trusted: critical rule must still gate → %s", d.Verdict)
	}
	// Block rules never loosen with band.
	if d := e.Evaluate(ctx, bashCall("b1", "rm -rf /")); d.Verdict != Deny {
		t.Errorf("trusted: block rule must still deny → %s", d.Verdict)
	}

	// Band changes are audited.
	var audits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='set_band'`).Scan(&audits)
	if audits != 2 {
		t.Errorf("set_band audits: %d, want 2", audits)
	}
	if err := SetBand(e.St, "a1", "bogus", "tester", "x"); err == nil {
		t.Error("invalid band must error")
	}
}
