// Package db is where the PostgreSQL pool, the transaction boundary and the
// read-only schema check shared by every context's adapters will live
// (F07 §5). It is empty until the first persistent slice lands.
package db
