package store

import (
	"crypto/rand"
	"math/big"
	"time"
)

// ID prefixes per CONTRACT.md §0.
const (
	PrefixSpace       = "sp_"
	PrefixMap         = "mp_"
	PrefixParticipant = "pt_"
	PrefixObject      = "ob_"
	PrefixArea        = "ar_"
	PrefixAgentKey    = "ak_"
	PrefixUser        = "us_"
	PrefixTicket      = "tk_"
	PrefixChatMessage = "cm_"
	PrefixSession     = "se_"
)

const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// newID returns prefix + 16 crypto/rand base36 characters.
func newID(prefix string) string {
	b := make([]byte, 16)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(36))
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = base36[n.Int64()]
	}
	return prefix + string(b)
}

// NewID is the exported ID generator for other packages.
func NewID(prefix string) string { return newID(prefix) }

// Now returns the current UTC time formatted as RFC3339 TEXT.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
