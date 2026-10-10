package main

import (
	"log"
	"os"

	"adb-fake-location/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
