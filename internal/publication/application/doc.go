// Package application holds the Publication use cases, starting with
// ActivatePublication, WithdrawPublication and Restore, and the authorized
// read GetAuthorizedDocument (F03 §9). A use case owns its transaction and
// reaches infrastructure only through the ports of this context.
package application
