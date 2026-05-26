package utils

import "maps"

func CopyMap[K comparable, V any](src map[K]V) map[K]V {
	if src == nil {
		return nil
	}

	dst := make(map[K]V, len(src))
	maps.Copy(dst, src)
	return dst
}
