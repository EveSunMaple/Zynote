//go:build !windows || !amd64

package im

func weixinSupported() bool { return false }
