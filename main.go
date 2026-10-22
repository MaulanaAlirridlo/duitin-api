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

	jwtSecret, err := jwtSecretFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	router := Router()
	Users(router, db, jwtSecret)
	authenticate := authMiddleware(jwtSecret)
	Categories(router, db, authenticate)
	Transactions(router, db, authenticate)
	Dashboard(router, db, authenticate)

	if err := router.Run(); err != nil {
		log.Fatal(err)
	}
}
