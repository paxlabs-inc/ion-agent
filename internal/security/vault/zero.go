package vault

import "runtime"

func zero(key []byte) {
	for index := range key {
		key[index] = 0
	}
	runtime.KeepAlive(key)
}
