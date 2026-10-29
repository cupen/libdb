package libdb

// Must 在 err 非 nil 时 panic，用于初始化路径。
func Must[T any](val T, err error) T {
	if err != nil {
		panic(err)
	}
	return val
}

// Must0 是 Must 的无返回值版本。
func Must0(err error) {
	if err != nil {
		panic(err)
	}
}
