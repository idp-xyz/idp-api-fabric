// Package domain is the Policy & Review model: PolicySnapshot, CheckRun,
// ReviewDecision and WaiverRef (F03 §3.1; INV-06, INV-07).
//
// Checks and decisions always reference a fixed policy snapshot. An approval
// is valid only for the content, policy, scope and period it was given for,
// and a waiver never covers a non-waivable rule. A passed check is evidence,
// not permission to publish.
package domain
