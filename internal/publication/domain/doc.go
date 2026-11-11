// Package domain is the Publication model: PublicationTarget, PublicationSlot,
// PublicationOperation, PublicationHistory and AccessBlock (F03 §3.1; INV-08,
// INV-09).
//
// A Target (Product × Audience × Environment × Portal) only scopes
// distribution. The Slot, unique per Target and APIAsset, holds the single
// active Release and a generation that every change must match. An effective
// AccessBlock denies reads and activations whatever the Slot points at. This
// context never changes Release content or identity-provider grants.
package domain
