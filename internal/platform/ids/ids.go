// Package ids generates the identifiers of Fabric's persistent objects. An
// identifier is stable, is never reused and says nothing about the object it
// names (F03 §1.3).
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// NewUUID returns a random UUID (RFC 9562, version 4) in its canonical form:
// 36 characters, lower case.
func NewUUID() string {
	var raw [16]byte
	// crypto/rand.Read does not fail: it ends the program if the system
	// cannot give it random bytes.
	_, _ = rand.Read(raw[:])
	raw[6] = raw[6]&0x0f | 0x40 // version 4
	raw[8] = raw[8]&0x3f | 0x80 // the RFC 9562 variant

	var text [36]byte
	hex.Encode(text[0:8], raw[0:4])
	text[8] = '-'
	hex.Encode(text[9:13], raw[4:6])
	text[13] = '-'
	hex.Encode(text[14:18], raw[6:8])
	text[18] = '-'
	hex.Encode(text[19:23], raw[8:10])
	text[23] = '-'
	hex.Encode(text[24:36], raw[10:16])
	return string(text[:])
}

// UUIDs generates random UUIDs. It is what the identifier ports of the
// bounded contexts are wired with.
type UUIDs struct{}

// NewID returns a new identifier.
func (UUIDs) NewID() string { return NewUUID() }
