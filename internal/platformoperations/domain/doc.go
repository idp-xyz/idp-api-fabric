// Package domain is the Platform Operations model: Job, AuditRecord, the
// outbox and inbox records and Reconciliation (F03 §3.1; INV-12, INV-13).
//
// A Job is completed only by the holder of its current lease, and a retry
// never produces a second effect. Every successful critical write records its
// audit fact and outbox event in the same transaction, append-only. Job and
// delivery status are operational evidence: they never decide what is
// published.
package domain
