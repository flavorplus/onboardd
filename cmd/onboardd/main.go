package main

import (
	"flag"
	"fmt"

	"github.com/jvermeulen/onboardd/internal/buildinfo"
)

func main() {
	showVersion := flag.Bool("version", false, "print version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("onboardd %s\n", buildinfo.Version)
		return
	}

	fmt.Println("onboardd: Phase 0 project scaffold")
}
