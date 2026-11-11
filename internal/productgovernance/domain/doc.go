// Package domain is the Product Governance model: Product, Workspace,
// WorkspaceMembership and ProductAudienceConfig (F03 §3.1; INV-01, INV-02).
//
// A Workspace belongs to exactly one Product, and membership is granted
// explicitly per Product, Workspace, Principal and Role. A Principal is a
// reference into an external identity provider: this context never issues
// tokens and never stores credentials.
package domain
