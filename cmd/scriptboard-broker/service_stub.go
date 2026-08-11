//go:build !windows

package main

func runAsWindowsService([]string) (bool, error) { return false, nil }
