// Package application holds the Platform Operations use cases, starting with
// ReconcilePublicationDelivery and, in V1, InspectDrift (F03 §9). A use case
// owns its transaction and reaches infrastructure only through the ports of
// this context.
package application
