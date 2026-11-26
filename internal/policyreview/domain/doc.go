// Package domain is the Policy & Review model: PolicySnapshot, CheckRun,
// the release gate and its waivers, and the ReviewDecision records of before
// (F03 §3.1; INV-06, INV-07).
//
// Checks always reference a fixed policy snapshot. A release rests only on
// the content a check read, a check under the current policy and the
// waivers it was prepared with (CR-0001), and a waiver never covers a
// non-waivable rule.
package domain
