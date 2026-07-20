//go:build !windows

package main

func runAsWindowsService(_ []string) (bool, error) { return false, nil }
