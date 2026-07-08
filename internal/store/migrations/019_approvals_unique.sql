-- Closes the findOrCreateApproval TOCTOU: two concurrent hook invocations for
-- the same (run_id, action) both SELECT no pending row, then both INSERT,
-- producing two approval rows for one logical decision (an operator approving
-- one leaves the other stuck pending forever). A partial unique index scoped
-- to status='pending' lets the same (run_id, action) be reused across
-- retries once a prior approval is decided/expired, while making concurrent
-- creation for the SAME pending action fail one of the two racing inserts.
CREATE UNIQUE INDEX idx_approvals_pending_run_action
  ON approvals(run_id, action) WHERE status = 'pending';
