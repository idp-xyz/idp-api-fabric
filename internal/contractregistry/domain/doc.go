// Package domain is the Contract Registry model: ContractSource,
// SourceSnapshot, ContractRevision, ContractRelease and their manifests
// (F03 §3.1; INV-04, INV-05).
//
// A Revision's content and digest never change; a change is a new Revision.
// A Release becomes READY only with matching review evidence and a readable,
// digest-verified artifact, and its content is immutable afterwards. Whether
// a Release is visible on any target is decided by Publication, not here.
package domain
