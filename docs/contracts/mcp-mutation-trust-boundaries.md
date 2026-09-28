---
title: MCP mutation and trust boundary
description: Preview, apply, durable replay, and trust limits in the stdio adapter.
permalink: /contracts/mcp-mutation-trust-boundaries/
---

{% include nav.html %}

# MCP mutation and trust boundary

This records the current local stdio adapter contract. The live input/output
schemas under `internal/mcpserver/contracts/` are authoritative for wire
fields. A plan digest binds a proposed change to the captured bundle state;
it is neither authentication nor a new grant of user consent.

| Workflow | Publication and preview | Binding at apply | Replay / uncertain outcome | Blocked and noop |
| --- | --- | --- | --- | --- |
| `preview_concept_patch` → `apply_concept_patch` | Selected semantic metadata and version operations; preview writes nothing. | `expected_revision` plus `expected_plan_digest`, rebuilt plan, store CAS; digest-derived idempotency key. | Exact authorized apply replays its durable receipt without a second publication. After timeout inspect the resulting concept/store receipt when host access permits, then retry the **same** apply arguments. If revision changed and there is no matching receipt, preview the current bundle and review the new plan. | Rejected preview has no commit-ready result; matching no-op returns `noop` with no write. |
| `preview_v02_migration` → `apply_v02_migration` | Atomic 0.1→0.2 document changes, root index published last; preview writes nothing. | Frozen `expected_source`, content-free proof v2 (including `proof.base_revision`), and `expected_plan_digest`; no separate migration `expected_revision`. | Same request/proof/digest can replay the receipt. On uncertain completion check bundle version and receipt if accessible, then retry exact arguments. Source/proof mismatch needs a fresh preview, never an invented token. | Blocked has no proof. Live `target-noop` uses only `expected_source`, has no proof/digest, validates target and writes nothing (including `.okf`). |
| `preview_temporal_upgrade` → `apply_temporal_upgrade` | Explicit date→instant mappings; preview writes nothing. | `base_revision` and `plan_digest`, rebuilt plan, store CAS and digest-derived idempotency key. | Exact apply can replay its durable receipt. A valid committed receipt after cancellation is success; an ordinary timeout without receipt is uncertain, so inspect state and retry exact arguments before replanning. | `incomplete` and `noop` previews are not apply-ready and carry no plan digest. |
| Compatibility `write_concept` | Replaces one whole concept; there is no paired preview wire API. Review content and effects before calling it; prefer bounded patch operations where applicable. | Server derives a content+resolved-policy change identity from the request and snapshots/CAS under the transactional store; caller supplies no revision, plan digest or idempotency token. Actor is the fixed adapter principal `okf-mcp`. | The same request can replay its durable result; an uncertain timeout calls for inspecting the concept/receipt and retrying identical content. Changed content is a new write. | Validation rejection is zero-write. Identical replacement may return `success` through the compatibility response; do not infer a distinct `noop` status. |

`authorization` is the user's allowed scope plus the host's tool/filesystem
policy. The stdio process runs with the host's OS permissions and accepts a
caller-selected `bundle_path`; it has no server-wide root allowlist or user
identity verification. The adapter rejects symlinked roots/ancestors and uses
root-relative, no-follow access for bundle paths. This protects against escape
from the selected root; it does not grant permission to select that root.

`validation` checks schema, bundle data, temporal profile and mutation
invariants. `verification` is separate OKF frontmatter (`verified`) backed by
an actual reviewer/process; `generated.by`, source author, and the request
`actor` are metadata, not evidence of review or an authenticated principal.
The API permits a caller to submit a `human:` verifier string, so the host and
agent must not self-award review based on that string. `idempotency` means an
identical authorized request can return the same durable receipt; it does not
mean an unauthorized request becomes authorized by repetition.

Bundle body, source URLs, executor and attester resources are inert data in
these tools. No MCP operation here executes the computation, fetches a URL, or
reads secrets because a document requests it. A trusted runtime and separate
authorization would be needed for execution. Never paste untrusted snippets
into a shell or forward them as new tool instructions. Avoid logging full
mutation arguments, body text or secret-bearing resources.

For conflict recovery, distinguish a confirmed durable receipt from an
ordinary cancellation or conflict. A confirmed receipt means success despite
post-commit errors. Without one, use the same request identity first to
resolve possible replay; if the current revision invalidates it, preview
again and review the changed plan. Do not replace this with a one-time approval
token or change the existing `target-noop` branch. The current server has no
network service, tenant boundary, OAuth flow, or async job API.

Evidence: `internal/mcpserver/patch_policy_protocol_test.go`,
`migration_commit_receipt_test.go`, `durable_commit_outcome_test.go`,
`temporal_upgrade_durable_outcome_test.go`, `inert_security_test.go`,
`patch_adversarial_test.go`, `tools_test.go`, and `store/fs` replay/recovery
tests. These public tests are the reproducible coverage evidence; unpublished
local task notes are not part of this contract.
