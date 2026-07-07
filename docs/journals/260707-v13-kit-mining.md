# v13 — Kit & Mining (260707)

Branch: main. Commit: TBD. Plan: `260707-2000-dandori-v13-kit-mining`.

## Biến hoạt động agent thô thành tri thức tuyển-lọc, phân phối được

Hành động: KHÔNG pipeline mới. Cưỡi lên envelope v12 (`knowledge_units` state machine) đã có, thêm 4 mảnh: **mining** (4 tín hiệu SQL on-demand tìm "run đáng đọc", không leaderboard) → **AI-draft** (LLM sinh nháp practice markdown từ evidence DB, redact 2 lượt, fail-open) → **kit** (bundle `.claude/*.md` người tự chọn thành 1 unit `kind=kit` xét-duyệt-nguyên-khối, Merkle-lite hash-pin, phân phối qua `kit pull`) → **import** (memory/journal `.md` có sẵn vào `kind=context` qua review gate, không viết lại). Mọi unit mang badge `origin` xuyên suốt.

**Hai đặc điểm nhất:**
- **Kit review nguyên khối, ranh giới = một hash manifest** (H1). Applier re-verify TOÀN BỘ file row so với manifest đã pin lúc `RequestPublish` trước khi publish — tamper một file sau khi request publish tự fail-closed, không cần cơ chế pin per-file rời.
- **Path safety deny-list-first, một package duy nhất** (`internal/kitpolicy`, H3) — cả nominate lẫn pull import lại, không có hằng số bảo mật lặp hay lệch giữa hai chỗ.

## Red-team + 2 vòng code-review

Brainstorm → plan 6 phase → red-team adversarial (2 CRITICAL + 3 HIGH + 5 MEDIUM + 3 LOW, tất cả xử lý trong-plan trước cook) → code-review P1-P3 (mining/import/draft) → code-review P4-P5 (kit distribution) → P6 E2E (phase này).

**Hai cú bắt thật của review** (không phải hình thức):

1. **C1 — `ApproveHash` hardcode `KindSkill`, dead-on-arrival cho kit.** Trước fix, hash-pin verify của MỌI kit lineage sẽ luôn build sai subject (`skill:<name>` thay vì `kit:<name>`) — nghĩa là `kit pull` không bao giờ verify đúng, tính năng kit coi như chết ngay khi chạm review thật dù mọi phase khác đã xong. Fix: `subject := u.Kind + ":" + u.Name` — một dòng, verify no-op cho 4 call-site skill hiện có (namespace tách biệt qua prefix, không có kind-confusion), auto-đúng thêm cho kit. Review P4/P5 xác nhận holds cả empirically lẫn qua test.
2. **C2 — origin/provenance bị handler âm thầm drop.** `handleKnowledgeNominate` đã có sẵn `NominateParams.ProvenanceRun` plumb tới INSERT từ trước, nhưng chính handler web chưa từng đọc `origin`/`origin_model`/`provenance_run_ids` từ form — mọi unit nominate qua web trước fix đều mất badge nguồn-gốc, và một `provenance_run_ids` giả (id không tồn tại trong `runs`) sẽ không bị chặn vì chưa có validate. Fix: đọc đủ 3 trường + validate mọi id qua `SELECT 1 FROM runs WHERE id=?`, reject NGUYÊN nominate (không partial-drop) nếu bất kỳ id nào forge. Test round-trip cố tình assert *các field khác không bị xê dịch vị trí cột* (`TestNominateOriginRoundTrip`) — bắt đúng cái bẫy column-count-trap, không chỉ "origin đọc lại đúng".

Cả hai đều là loại lỗi review bắt được TRƯỚC khi tới E2E — đúng lý do có 2 vòng code-review tách biệt cho plan này.

## Phantom coverage lặp lại — lần này ở ranh giới git thật

Bài học phantom coverage của v12 (mọi test hand-craft evidence, không đi full path) lặp lại dưới hình dạng khác ở P6: **mọi test kit từ P4 trở đi đều stub `gitLsFiles`** để trả sẵn bare path (`agents/reviewer.md`, không prefix) — hợp lý để test nhanh, không phụ thuộc git binary. Nhưng phase-06 yêu cầu rõ Loop 1 phải `git-init temp repo` thật, và khi viết đúng theo yêu cầu đó (không stub), lộ ra: `git ls-files -- .claude` thật trả path **repo-root-relative** (`.claude/agents/reviewer.md`), khác hẳn quy ước `.claude`-relative mà `kitpolicy.ValidateKitPath` và toàn bộ pipeline nominate giả định.

Hậu quả trước khi vá: `kit nominate` chạy trên BẤT KỲ repo git thật nào sẽ thấy segment đầu là `.claude` — không nằm trong whitelist (`agents/rules/skills/commands`) — nên lặng lẽ loại bỏ HẾT mọi file, báo "không còn file nào sau khi lọc" dù `.claude/agents/*.md` được track đầy đủ. Tính năng **chưa từng chạy được thật ngoài môi trường test** cho tới khi E2E chạm đúng seam git thật — không một test nào trong 10 test `kit_cmd_test.go` cũ có thể bắt được vì tất cả đi qua đúng một chỗ giả giống nhau.

Vá: `stripClaudePrefix` ngay sau lời gọi `gitLsFiles` trong `runKitNominate` (pass-through cho path đã bare, không phá stub cũ), cộng đường đọc file trên đĩa re-add đúng segment `.claude` mà `repoRoot` không bao gồm — cùng quy ước `skillreg.KitLocalPath` phía pull đã dùng từ đầu. `writeRepoFile` (test helper) sửa theo để ghi đúng `repoRoot/.claude/<rel>`, giữ nguyên toàn bộ 10 test cũ xanh.

## Parallel execution + gate giữa chừng

- **P1 (mining + migration 017)**: 4 tín hiệu SQL, `mining_dismissals` reading-list-only.
- **P2 ∥ P3**: P2 import CLI + origin/provenance handler fix (C2); P3 AI-draft (single-flight H2, fail-open). Không đụng file chung — P3 post vào handler P2 sở hữu, không tự sửa.
- **P4 → P5 tuần tự** (không song song): P4 tạo `internal/kitpolicy` + kit nominate; P5 import lại kitpolicy cho `KitLocalPath` + sở hữu fix C1. P5 phụ thuộc trực tiếp shape manifest P4 định nghĩa.
- **P6 (E2E + docs, phase này)**: Loop 1 (kit full round-trip, real git, 2 temp repo) + Loop 2 (mining→draft→nominate, tham chiếu code có sẵn từ P1-P3) + Loop 3 (import, tham chiếu THUẦN — `import_cmd_test.go` đã phủ đúng contract, không viết thêm) + security negatives.

## Fleet thật — chỉ ~12 Skill event, ship infra với empty-state trung thực

Giống v12 (`MinSampleForKnowledge=10` chỉ vừa đủ ngưỡng với ~12 Skill event/fleet), mining + kit ở v13 ship như **hạ tầng**, không phải tính năng có dữ liệu dày để trình diễn ngay: 4 tín hiệu mining sẽ trả về ít hoặc rỗng trên fleet hiện tại (chưa đủ run mang corrective-steering≥2 hay cost-outlier rõ rệt), kit chưa có bundle nào được nominate thật ngoài fixture test. Đây là quyết định đúng đắn nhất quán — không bơm dữ liệu giả để trang mining/kit "trông có vẻ sống", để trống trung thực và tự dày lên khi fleet dùng thật.

## Verify

`CGO_ENABLED=0 go build ./...` sạch. `go vet ./...` sạch. `go test ./... -count=1` xanh toàn repo (18 package, gồm `internal/cli` 17.2s + `internal/web` 14.4s — hai suite lớn nhất, liên quan trực tiếp v13). `GOOS=linux CGO_ENABLED=0 go build ./cmd/dandori` cross-compile thành công (binary 23MB).

## Settled deviations

- **Kit scan git-tracked-only** — không phải giới hạn, là kỷ luật bảo mật cố ý (file `.md` nhạy cảm chưa commit không bao giờ vào kit).
- **`permission_ask`-denied làm tín hiệu mining thứ 5** — loại khỏi MVP ngay từ brainstorm, để [Sau] (v13.1).
- **Không per-file diff side-by-side khi kit version bump** — chỉ danh sách new/changed/match ở MVP, per plan's cut-line.
- **KHÔNG contributor leaderboard cho kit** — nhất quán quyết định đã khoá ở v12 ("NO contributor leaderboard"), áp dụng sang cả kit/mining.

## Deferred / [Sau]

- Central-mode kit distribution (cùng nhóm gap với v12's central-mode).
- `kit pull --prune` dọn file mồ côi (file pull từ bản kit cũ, bị bỏ ở bản mới) — cố ý chưa làm, an toàn hơn tự xoá nhầm.
- Per-file diff polish khi kit version bump (hiện chỉ new/changed/match).
- `permission_ask` mining signal (v13.1).
- Recognition/ghi nhận cho người tạo kit hay nominate tri thức được publish.

## Bug production tìm-và-vá ngay trong phase này (không phải [Sau])

`.claude/` prefix mismatch giữa `git ls-files` thật và `kitpolicy.ValidateKitPath`'s convention — xem mục "Phantom coverage" ở trên. Đã vá trong `internal/cli/kit_cmd.go` (`stripClaudePrefix` + đường đọc file), không phải một gap để lại cho version sau.

**Status:** DONE
**Summary:** v13 khép thêm một nhánh của LEARN — mining (4 tín hiệu on-demand) + AI-draft (fail-open, evidence-only) + kit bundle (Merkle-lite, deny-list-first path-safety) + import (CLI-explicit) — toàn bộ cưỡi lên envelope v12, không pipeline mới, không số bịa. Hai vòng code-review bắt đúng 2 lỗi thật (C1 ApproveHash dead-on-arrival cho kit, C2 origin/provenance bị handler âm thầm drop) trước khi tới E2E; P6 tự bắt thêm một bug production thật (`.claude/` prefix mismatch trong `kit_cmd.go`) nhờ cố tình dùng git thật thay vì stub — phantom-coverage lesson của v12 lặp lại dưới dạng khác, cùng bài học: mọi test đi qua đúng một seam giả sẽ không bao giờ bắt lỗi nằm ở chính ranh giới đó. Build/vet/test/cross-compile xanh toàn repo.
**Concerns:** Fleet chỉ ~12 Skill event — mining/kit ship như hạ tầng honest-empty-state, chưa có dữ liệu dày để trình diễn. Kit chưa có bundle thật nào ngoài fixture. Central-mode kit distribution + `--prune` để [Sau].
