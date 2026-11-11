//go:build windows

package main

func syscallUmask(int) int { return 0 }
