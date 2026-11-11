//go:build !windows

package main

func preventIdleSleep() (func() error, error) { return func() error { return nil }, nil }
