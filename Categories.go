package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type categoryInput struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func Categories(router *gin.Engine, db *sql.DB) {
	router.POST("/categories", createCategory(db))
	router.GET("/categories", listCategories(db))
	router.GET("/categories/:id", getCategory(db))
	router.PUT("/categories/:id", updateCategory(db))
	router.DELETE("/categories/:id", deleteCategory(db))
}

func createCategory(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input categoryInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if message := validateCategoryInput(&input); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		var category Category
		err := db.QueryRowContext(c.Request.Context(),
			`INSERT INTO categories (name, type, created_at)
			 VALUES ($1, $2, CURRENT_TIMESTAMP)
			 RETURNING id, name, type, created_at`,
			input.Name, input.Type,
		).Scan(&category.ID, &category.Name, &category.Type, &category.CreatedAt)
		if err != nil {
			log.Printf("create category: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create category"})
			return
		}
		c.JSON(http.StatusCreated, category)
	}
}

func listCategories(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.QueryContext(c.Request.Context(),
			`SELECT id, name, type, created_at
			 FROM categories
			 ORDER BY id`,
		)
		if err != nil {
			log.Printf("list categories: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list categories"})
			return
		}
		defer rows.Close()

		categories := make([]Category, 0)
		for rows.Next() {
			var category Category
			if err := rows.Scan(&category.ID, &category.Name, &category.Type, &category.CreatedAt); err != nil {
				log.Printf("read category: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read categories"})
				return
			}
			categories = append(categories, category)
		}
		if err := rows.Err(); err != nil {
			log.Printf("iterate categories: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read categories"})
			return
		}
		c.JSON(http.StatusOK, categories)
	}
}

func getCategory(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseCategoryID(c)
		if !ok {
			return
		}

		category, err := findCategory(c, db, id)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		if err != nil {
			log.Printf("get category %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get category"})
			return
		}
		c.JSON(http.StatusOK, category)
	}
}

func updateCategory(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseCategoryID(c)
		if !ok {
			return
		}

		var input categoryInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if message := validateCategoryInput(&input); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		var category Category
		err := db.QueryRowContext(c.Request.Context(),
			`UPDATE categories
			 SET name = $1, type = $2
			 WHERE id = $3
			 RETURNING id, name, type, created_at`,
			input.Name, input.Type, id,
		).Scan(&category.ID, &category.Name, &category.Type, &category.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		if err != nil {
			log.Printf("update category %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update category"})
			return
		}
		c.JSON(http.StatusOK, category)
	}
}

func deleteCategory(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseCategoryID(c)
		if !ok {
			return
		}

		result, err := db.ExecContext(c.Request.Context(),
			`DELETE FROM categories WHERE id = $1`,
			id,
		)
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23503" {
				c.JSON(http.StatusConflict, gin.H{"error": "category is used by one or more transactions"})
				return
			}
			log.Printf("delete category %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete category"})
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			log.Printf("check deleted category %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete category"})
			return
		}
		if rowsAffected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func findCategory(c *gin.Context, db *sql.DB, id int64) (Category, error) {
	var category Category
	err := db.QueryRowContext(c.Request.Context(),
		`SELECT id, name, type, created_at
		 FROM categories
		 WHERE id = $1`,
		id,
	).Scan(&category.ID, &category.Name, &category.Type, &category.CreatedAt)
	return category, err
}

func parseCategoryID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return 0, false
	}
	return id, true
}

func validateCategoryInput(input *categoryInput) string {
	input.Name = strings.TrimSpace(input.Name)
	input.Type = strings.TrimSpace(input.Type)

	if input.Name == "" {
		return "name is required"
	}
	if utf8.RuneCountInString(input.Name) > 100 {
		return "name must be at most 100 characters"
	}
	if input.Type != "income" && input.Type != "expense" {
		return "type must be either income or expense"
	}
	return ""
}
