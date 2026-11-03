# Evidence pipeline v2.1 implementation status

This is an incremental implementation. It does not yet claim the full v2.1 design or measured latency/accuracy improvements.

## Available behavior

- Course correction uses at most two active calls and an admitted/uncommitted window of four, with source-order checkpoint commits.
- Both correction and derived subtitle summaries use a persistent physical-attempt ledger. Policy v1 caps one stable job at 256 physical requests across retries, JSON repair and resume. Tokens and provider billing are unknown; byte/count guards are not monetary guarantees. Complete responses without usage spend the attempt cap; dispatched errors, 429 and unconfirmed results retain UNKNOWN liability. Such UNKNOWN calls do not automatically retry. There is currently no user-facing manual release/settlement action; the app must not imply a recovery button exists.
- Stable correction identity includes lecture/source fingerprint, exact batch rows, the exact request builder's endpoint/body digest (including actual selected images/context and model parameters), JSON repair policy and a versioned correction recipe. Validated successes are cached separately from committed progress. Resume can explicitly adopt matching successes; source/request/recipe/epoch mismatch prevents adoption. One entry is limited to 4 MiB and a job's live success cache to 64 MiB.
- A cross-process OS lease prevents live task takeover. Process death releases it. SQLite is the execution-generation authority; generation rollover and guarded local task/publication mutations share a write serialization point.
- New subtitle summary recipe v2 uses half-open Unicode codepoint core spans. Subcue text inherits the full parent cue time; no word timing is invented. Observer protocol v3 records explicit processing receipts independently of cards. Complete with zero cards is valid; insufficiency marks the published summary as partial. Processing coverage is not semantic completeness.
- Existing frozen recipe v1 tasks continue using their original prompt, cue chunking and observer protocol, including uncalled items after a completed item. Old v1/v2 audit interpretation remains supported.
- Nonstream and stream responses have an 8 MiB cumulative wire-byte limit, JSON depth64 and string1MiB limits before JSON allocation. SSE completion checks remain required. Read errors do not expose transport details.

## Remaining work

The new summary pipeline is currently sequential. Concurrent summary receipts, cross-generation summary success adoption, bounded global reduction with recomputable leaf dispositions, targeted verification, product-facing cache-revocation/explicit reauthorization operations and benchmark ablations remain pending. The internal revoke/tombstone transaction is tested, but is not yet a product action. Cancellation is not permanent revocation. No token hard limit is asserted without a verified tokenizer/usage adapter.
