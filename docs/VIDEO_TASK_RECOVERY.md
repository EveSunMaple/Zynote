# Video task queue and recovery

## Implemented boundary

`jobs.Registry.RunQueued` is a reusable bounded FIFO scheduler sharing the
existing global exclusive worker. The first integration is
`subtitle-derived:start/resume`; other job entry points still return BUSY.
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

## Model checkpoint policy

Before invoking the model, the domain synchronously persists `dispatched`.
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
  the actual queue, kills it at dispatched and prepared checkpoints, then
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
