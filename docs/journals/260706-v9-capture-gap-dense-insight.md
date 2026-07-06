# v9 — Capture-gap + dense-insight (260706)

Commit `1d334f3`. Plan `260706-2030-dandori-v9-capture-gap-dense-insight`.

## The inversion

Started as a grade-formula overhaul. Red-team (verify bằng sqlite3 trên 36 run thật) killed that shape: the formula was designed for inputs that **don't exist in real data** — `task_key` 0/36, `lines_added` 0/36, positive-duration 3/36, `user_msg` chỉ đếm, guardrail events 0/0/0. Reweighting a composite over empty columns = laundering missing-data into numbers.

Đảo thứ tự: **vá capture trước (Milestone A), insight trên data đã dense (Milestone B), hoãn công thức sang v10.** Không ship formula trên data trống.

## Fixed (Milestone A)

- **Watcher end-time** — `ended_at = file mtime` (nguồn 31 duration âm) → timestamp event thật trong transcript. Gate `LastTS > FirstTS`, thiếu → NULL không bịa. `watch --full` backfill. **3→30 dương, 31→0 âm.**
- **task_key** — chain branch→commit→transcript, validate với `work_items` (không match → bỏ, không đoán). **0→1 validated, 0 mislink** — honest-low vì dev chạy trên `main`, không có Jira key.
- **git-delta watcher** — snapshot start + delta close. Lịch sử KHÔNG backfill được (before-state mất); run mới capture đúng.
- **steering capture** — `user_msg` chỉ-đếm → thêm event text (redacted). **0→29 run.** KHÔNG classify (để v10).

## The two "honest zero" findings

Điều bất ngờ nhất: **guardrail 0/0/0 KHÔNG phải capture gap.** Producers (`engine.record`, `KillRun`) đều chạy — chứng minh bằng **live E2E** (`hook pre-tool` với `rm -rf /` → deny + event + audit thật trong DB). Zero là honest: fleet chưa vi phạm **+ hook chưa cài trong `.claude/settings.json`**.

Cùng root cause — **hook chưa cài** — giải thích luôn git-delta 0-lines (`head_before` NULL 7/7 hook-run). Không phải bug, là setup gap. Docs push "cài hook" thành bước đòn bẩy cao nhất (unblock guardrail + git-delta + end-time cùng lúc).

Bài học: trước khi "sửa" một cột rỗng, phân biệt **capture-gap** (đã fix) vs **honest-zero** (hệ thống đúng, thực tế bằng 0). Nhầm hai cái này = sửa nhầm hoặc bịa số.

## Shipped (Milestone B)

`/insights` — model efficiency (cost/done, cache-hit), cache utilization, cost-per-outcome theo project/agent. Descriptive stats thuần, KHÔNG grade/weight. Bất biến "insufficient ≠ so-sánh-được": ngưỡng `n<3` in rõ trên UI, badge "chưa đủ mẫu", cost/done hiện tử/mẫu. Server-render + HTMX, không SPA.

## Gate v10

Density đủ để *bắt đầu tích luỹ* nhưng task_key + git-delta lịch sử vẫn mỏng (data mất). v10 (formula overhaul) chờ run MỚI (đã cài hook) tích luỹ; reweight phải show before/after ranking delta thật, delta ~0 thì hoãn.

## Verify

Code-reviewer clean (0 Critical/High, 6 invariants test-backed). 1 Low fix applied: `TopCacheRuns` best/worst disjoint trên fleet nhỏ. `go test ./...` + `go vet` xanh. Live E2E: insights page HTTP 200 trên data thật, guardrail hook trigger recorded.
