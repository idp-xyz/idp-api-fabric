// Package domain is the Product Governance model: Product, Workspace,
// WorkspaceMembership and ProductAudienceConfig (F03 §3.1; INV-01, INV-02).
//
// A Workspace belongs to exactly one Product, and membership is granted
// explicitly per Product, Workspace, Principal and Role. Who owns a Product
// is granted as explicitly, per Product, Principal and Role. Which audiences
// a Product documents its APIs for, and in which environments, is configured
// as explicitly too, and a Product starts with none. A Principal is a
// reference into an external identity provider: this context never issues
// tokens and never stores credentials.
package domain
