package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatal(fmt.Errorf("load .env: %w", err))
	}

	db, err := openDatabase()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	router := Router()
	Categories(router, db)
	Transactions(router, db)
	Dashboard(router, db)

	if err := router.Run(); err != nil {
		log.Fatal(err)
	}
}
