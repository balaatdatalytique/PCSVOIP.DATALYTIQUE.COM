package main

import (
	"log"

	"pcsvoip-cms/internal/config"
	"pcsvoip-cms/internal/server"
)

func main() {
	cfg := config.Load()

	if err := server.Run(cfg); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
