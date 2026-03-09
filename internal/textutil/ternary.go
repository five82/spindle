package textutil

// Ternary returns a if cond is true, otherwise b.
func Ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
