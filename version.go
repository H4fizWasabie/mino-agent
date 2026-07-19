package main

import (
	_ "embed"
	"fmt"
	"os"
	"runtime"
)

// Version is set at build time via -ldflags "-X main.Version=v0.2.0"
var Version = "dev"

//go:embed go.mod
var goMod []byte

func printVersion() {
	fmt.Printf("mino %s %s/%s\n", Version, runtime.GOOS, runtime.GOARCH)
}

// currentExe returns the path of the running binary
func currentExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "mino"
	}
	return exe
}
