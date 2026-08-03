//go:build !darwin

package main

func requestAttention(critical bool) {}
func cancelAttention()               {}
func bringToFront()                  {}
