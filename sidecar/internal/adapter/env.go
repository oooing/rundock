package adapter

import "os"

// osLookupEnv 封装 os.LookupEnv，便于测试时替换。
func osLookupEnv(key string) string {
	v, _ := os.LookupEnv(key)
	return v
}
