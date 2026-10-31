# Video task queue and recovery

## Implemented boundary

`jobs.Registry.RunQueued` is a reusable bounded FIFO scheduler sharing the
existing global exclusive worker. The first integration is
`subtitle-derived:start/resume`, local video transcription, and persisted
subtitle resume, remote subtitle acquisition/transcription, and PPT
source/auto/extract/export. Other job entry points still return BUSY.
There are at most 16 queue participants, including the running queued task.
Normal jobs and queued jobs cannot execute simultaneously. Cancellation applies
to both waiting participants and the active job.

Domain inputs are saved in `Library/NoteGeneration/<task-id>.json` before queue
admission. Queue metadata is saved in `Library/TaskQueue/<task-id>.json`, using
the same stable task ID. It contains only ID, kind, state and update time; no
closures, subtitle text, credentials or provider responses. Writes use a unique
temporary file, file sync, close and rename. This does not claim power-loss
atomicity of the directory or a multi-file transaction.

The start action optionally accepts `operationId` (UUID). The UI retains it
across a failed submission and resets it when the selected inputs change. The
domain binds that ID to a SHA256 of normalized sources, ordered modes and course;
identical resubmissions preserve checkpoints, while changed inputs cannot reuse
the ID. Older clients without this field still get a newly generated task ID
and should use the existing resume action after interruption.

Queue ownership is process-local. After backend loss, a queued/running record
without a live owner projects as `interrupted`. The existing explicit resume
action reconstructs domain work using its persisted inputs. Restart never
silently replays a model request. This is not automatic boot-time dispatch or
a detached request protocol: callers still await their existing RPC result.
The scheduler assumes the existing single backend process per library; it is
not a multi-process distributed lock.

The batch records view displays queue status. Per-task progress keys use
`subtitle-derived:<task-id>` to avoid conflating distinct jobs. Task list refresh
reads the persisted queue projection; source files and outputs remain subject
to domain hash/version validation.

Local transcription persists its existing copied-video task before queue
admission. Resume validates the UUID and existing subtitle-task identity before
admission, and re-reads the task when execution starts. Its progress key is
`subtitle-transcribe:<task-id>`. Pending-video records project queue state into
the task center. Cancelling while queued retains the source and recovery copy;
an API regression verifies no ASR runs while another worker owns the slot.
Remote transcription persists its source URL/task metadata before admission;
download and ASR share the same exclusive worker. Local/remote starts accept
operationId and bind source digest/URL plus course. Local same-ID retry reuses
the selected source without reopening the picker. Picker cancellation creates
no binding; successful/cancelled UI selections release their draft, and a failed
selection offers explicit continue versus choose-new actions. Completed subtitle
receipts return the same hash-verified SRT without ASR. Before publishing, a
synced prepared-SRT checkpoint binds the response to the video digest. Retrying
publication reuses its journaled target path; recovered older temporary audio is
preserved with a warning rather than deleted from an untrusted persisted path. A queued cancellation retains
the source record. Explicit subtitle resume can reacquire a missing new bound
copy under the queue. Corrupt/unverified copies fail closed; historical partial
files are not silently adopted or deleted.

New remote, local subtitle and PPT video copies use same-directory staging,
file sync and close before atomic no-overwrite hard-link publication. Failed or
cancelled copies do not expose `video.mp4`; normal error exits remove staging.
A process kill may leave a hidden `.video-copy-*` staging file, which resume
does not treat as video. Filesystems without hard links fail closed. Directory
power-loss durability and concurrent mutation of the source are not guaranteed.
Copies with a readiness record verify the expected digest before reuse. The
source digest is journaled before publication, allowing recovery after the link
without adopting arbitrary bytes. Already-existing legacy `video.mp4` files with
no readiness proof are preserved and remain unverified; this change does not
certify or repair historical partial subtitle copies.

PPT task inputs are persisted before admission. All stages of a task retain the
same `video-ppt:<task-id>` queue key, and content-addressed input records preserve
explicit crop/prompt revisions. Source/auto starts accept operationId and bind it to stage, normalized remote
URL or local source digest/path, prompt and course. The UI retains that identity
across same-input retry. An early task without source.json restores a resume
plan; explicit continuation submits only the operation ID and reconstructs the
bound inputs. A verified local recovery copy does not recopy a changed original.
PPT stage completion projects as awaiting review, rather than workflow done.

PPT source/extract model calls save synced, content-addressed dispatched/prepared
journals bound to the exact prompt/images and a non-secret provider/model/options
fingerprint. Prepared requests reuse their own response; explicit changed crops
can make a new revision, but any unresolved dispatch of the stage blocks all
revisions. Existing manual fallback warnings still surface model errors; those
warnings do not authorize replay. PDF export persists the selected destination
and expected digest before synced atomic no-overwrite publication. Retrying the
same export reuses that destination, while changed/deleted/user-modified outputs
are never silently overwritten. This protects publication before task cleanup;
a synced hash-verified completion receipt survives task cleanup. Auto retries
return that saved result without model calls. Source-only retry after a later
manual export instead reports the already-completed output path; it does not
reconstruct a deleted preview or repeat paid work.

Visual recognition also saves a synced per-frame unknown marker before provider
invocation. Provider errors/cancellation after dispatch remain unknown and cannot
be resumed into another call. Already-saved frame responses can recover output
without recognition. The subprocess-kill regression uses a simulated provider;
it does not call a paid model. Visual entries still use BUSY rather than FIFO.
`TestKilledPPTExportReusesPublishedPDF` kills a real subprocess after production
frame rendering/PDF building/verification/publication, then verifies same-path
recovery and exactly one PDF. Its frames are generated; no vision provider runs.
The recorded English native test explicitly selects English. The current video
API still uses its existing zh setting; that test is not UI English acceptance.

## Model checkpoint policy

For batch course-note generation, before invoking the model the domain
synchronously persists `dispatched`.
When a response arrives, it persists `prepared` and the result before export.
An interrupted `dispatched` item without a prepared response stays unknown and
cannot be automatically retried. Prepared results and verified outputs resume
without a new model call. The response-arrival/checkpoint gap remains unknown,
not assumed successful. No reconciliation UI is added by this change.

## Verification

- `go test -race ./internal/jobs ./internal/subtitlederived ./internal/api`
  covers FIFO order, capacity, cancellation, duplicate admission, identity
  binding, domain BUSY errors, and queue ownership after reconstruction.
- `TestKilledProcessDoesNotReplayGeneration` launches a separate process through
  the actual queue, kills it at queued, dispatched and prepared checkpoints, then
  explicitly resumes the same task. Its model response is simulated.
- `ZYNOTE_ASR_TEST=1 go test ./internal/asr -run TestRecordedSpeechPipeline -v`
  requires absolute `ZYNOTE_TEST_VIDEO`, `ZYNOTE_TEST_MODEL`, and
  `ZYNOTE_TEST_NATIVE` paths, plus ffmpeg on PATH. It runs real decode, native
  VAD/Whisper and SRT validation. Optional `ZYNOTE_TEST_EXPECT` checks a known
  phrase; `ZYNOTE_TEST_SRT_OUTPUT` writes local evidence. Default CI skips this
  opt-in test; it does not download media or weights.
- Acquire pinned models with `pnpm acquire:asr`, then validate local hashes with
  `node scripts/acquire-asr-model.mts --verify-local`. Large assets are not
  committed.
- `ZYNOTE_MODEL_TEST=1 go test ./internal/api -run TestRealModelRecordedCourseNotes -v`
  explicitly incurs provider usage. Supply process-only `ZYNOTE_TEST_API_KEY`,
  `ZYNOTE_TEST_MODEL_ID`, `ZYNOTE_TEST_BASE_URL`, and `ZYNOTE_TEST_SRT`; optional
  `ZYNOTE_TEST_NOTES_OUTPUT` saves local evidence. Do not commit credentials.
  The test uses the production model/citation pipeline and queued domain export,
  then verifies that a completed task resumes without entering summarization.

## Recorded speech acceptance sample

Local acceptance on 2026-10-31 used the 60–100 second excerpt of MIT OCW,
6.0001 Fall 2016, Lecture 1: What is Computation? (Ana Bell).

- Page: https://ocw.mit.edu/courses/6-0001-introduction-to-computer-science-and-programming-in-python-fall-2016/resources/lecture-1-what-is-computation/
- Official download: https://archive.org/download/MIT6.0001F16/MIT6_0001F16_Lecture_01_300k.mp4
- License: CC BY-NC-SA 4.0, https://creativecommons.org/licenses/by-nc-sa/4.0/
- Clip modification: trimmed and re-encoded to 40 seconds for local testing.
  Neither media nor derived transcript is distributed in this repository.
- Actual native pipeline returned 16 cues in about 42 seconds on Windows.
  Against wholly contained official VTT cues, case/punctuation-normalized word
  edit distance was 16/120 (13.3%); boundary cue differences are included.
  Two phrase-start comparisons: computation +0.23 seconds; Python -0.13 seconds.

This sample validates actual English speech handling. It does not establish
full-course, multilingual, long-video, or all-entry acceptance.

Actual provider acceptance used the returned model ID `deepseek-flash` (the
provider's model catalog did not expose the requested spelling
`deepseekv4.1flash`). All four note modes passed: brief/detailed/keypoints/mindmap
returned 4/10/8/7 verified points respectively in a 23.41-second test run.
Markdown/JSON and mindmap SVG were saved; explicit completed-task recovery made
no further summarizer invocation. The domain requires a Bilibili locator, so
this test explicitly uses a synthetic locator as a domain fixture; it does not
validate Bilibili acquisition or claim the MIT recording came from that URL.
The real content source and license are recorded separately in the report.

Expanded 12-minute acceptance exposed a real failure at VAD offset 673.904s:
a 0.832s speech range produced a model end timestamp of 2.0s in its padded
decoding window. Valid cue ends are now bounded to actual audio duration;
invalid starts, reversed/nonfinite spans, or spans beyond a full padded window
remain errors. The original 0.832s recording passed a real native regression
with a cue bounded to 0.000–0.832s. Full 12-minute rerun is recorded separately;
the short regression alone does not establish that longer acceptance passed.

The subsequent full 12-minute rerun passed in 411 seconds: 275 cues, all finite,
bounded and monotonically non-overlapping, last cue end 718.856s of 720s.
Against wholly contained official VTT cues, normalized word edit distance was
164/1912 (8.58%, including boundary differences). This verifies this English
course excerpt after the repair; full-course/multilingual acceptance remains
outside the recorded evidence.


## Public source projection and subtitle fallback

Bound PPT/subtitle operations project an allowlisted `MediaSource` through
pending tasks and `job:current`. Bili `resolved` means the catalog supplied the
matching BVID/CID/page; only a nonempty SHA256 means the current video copy
passed independent readiness verification. Local sources retain their bound
absolute path and digest. Signed transfer URLs, headers, cookies and catalog
payloads never enter this projection. Legacy tasks without an operation omit
this optional metadata.

Pending projection currently rehashes each ready video, so listing many large
videos has linear disk I/O cost. This correctness-first slice does not cache
trust using modification time; queue workers also verify before use. A bounded
cache and a measured large-video refresh budget remain follow-up work.

Batch subtitle acquisition falls back to video download/ASR only for the typed
`NO_SUBTITLE` outcome from an explicitly empty subtitle-track array in a
successful catalog response. Missing/null catalogs, login/rate-limit/network
failures and invalid/untrusted subtitle tracks remain errors. The renderer
retains its compatible no-subtitle message handling because the current shell
bridge carries error messages without structured error codes.

Legacy `video-ppt:batch` and `video-ppt:batch-subtitles` still use their outer
exclusive worker, and batch PPT model calls lack the new per-operation model
journal. They must not call the queued single-task entry from inside that worker
(which would deadlock); safe batch orchestration remains a separate migration.
