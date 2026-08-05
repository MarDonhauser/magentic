//go:build !darwin

package main

func micInUse() bool {
	return false
}
