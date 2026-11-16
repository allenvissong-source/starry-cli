package transport

import "strconv"

// ValidUserID accepts the canonical decimal wire representation of an int64 UID.
func ValidUserID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == id
}
