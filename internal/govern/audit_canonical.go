package govern

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// canonicalHash replaces a delimiter-joined hash (vulnerable to byte-shifting
// across field boundaries — two different field tuples that happen to
// concatenate to the same string produce the same hash) with a length-
// prefixed encoding. Each field is preceded by its byte length as a fixed
// 8-byte big-endian integer, so no rearrangement of bytes across field
// boundaries can produce the same digest: the length prefixes pin down
// exactly where one field ends and the next begins, and that boundary
// information is itself hashed.
func canonicalHash(prevHash, ts, actor, action, subject, detail string) string {
	h := sha256.New()
	for _, f := range []string{prevHash, ts, actor, action, subject, detail} {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(f)))
		h.Write(lenBuf[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}
