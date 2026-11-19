//go:build !unix

package main

func untrusted(path string) string { return "" }
