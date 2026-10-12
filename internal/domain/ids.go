package domain

import "crypto/rand"

// ID prefixes identify the resource type of an ID at a glance.
const (
	PrefixProject      = "prj"
	PrefixDeployment   = "dep"
	PrefixOperation    = "op"
	PrefixToken        = "tok"
	PrefixRequest      = "req"
	PrefixInstallation = "inst"
)

// idAlphabet is Crockford's base32 in lowercase: unambiguous, URL-safe, and
// valid in Docker resource names and DNS labels.
const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns a random identifier such as "dep_k7m2q9x4abcd" with 60 bits
// of entropy, which is ample for a single installation.
func NewID(prefix string) string {
	return prefix + "_" + RandomString(12)
}

// RandomString returns n random characters from the ID alphabet.
func RandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b) // Never fails; see crypto/rand.Read.
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}
