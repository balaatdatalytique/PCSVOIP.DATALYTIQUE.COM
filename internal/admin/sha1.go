package admin

import "crypto/sha1"

func sha1Sum(s string) [20]byte {
	return sha1.Sum([]byte(s))
}
