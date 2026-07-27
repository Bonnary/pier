package main

import (
	"fmt"
	"os"
)

const Version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("pier %s\n", Version)
		return
	}
	fmt.Println("pier: no command specified")
	os.Exit(1)
}
