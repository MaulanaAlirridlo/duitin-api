package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
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
	Icon      string    `json:"icon"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

type categoryInput struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
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
			`INSERT INTO categories (user_id, name, type, icon, color, created_at)
			 VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
			 RETURNING id, name, type, icon, color, created_at`,
			c.GetInt64(userIDContextKey), input.Name, input.Type, input.Icon, input.Color,
		).Scan(&category.ID, &category.Name, &category.Type, &category.Icon, &category.Color, &category.CreatedAt)
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
		page, ok := parsePageOptions(c)
		if !ok {
			return
		}
		filters := sqlFilters{}
		filters.add("user_id = ?", c.GetInt64(userIDContextKey))
		if categoryType := strings.TrimSpace(c.Query("type")); categoryType != "" {
			if categoryType != "income" && categoryType != "expense" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "type must be either income or expense"})
				return
			}
			filters.add("type = ?", categoryType)
		}
		filters.addSearch("name", c.Query("search"))

		var total int64
		if err := db.QueryRowContext(c.Request.Context(),
			"SELECT COUNT(*) FROM categories"+filters.where(),
			filters.args...,
		).Scan(&total); err != nil {
			log.Printf("count categories: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not count categories"})
			return
		}

		rows, err := db.QueryContext(c.Request.Context(),
			`SELECT id, name, type,
			        COALESCE(icon, CASE type WHEN 'income' THEN '💰' ELSE '📦' END),
			        COALESCE(color, CASE type WHEN 'income' THEN '#10b981' ELSE '#f59e0b' END),
			        created_at
			 FROM categories`+filters.where()+`
			 ORDER BY id
			 LIMIT $`+strconv.Itoa(len(filters.args)+1)+` OFFSET $`+strconv.Itoa(len(filters.args)+2),
			filters.argsWith(page.Limit, page.Offset)...,
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
			if err := rows.Scan(&category.ID, &category.Name, &category.Type, &category.Icon, &category.Color, &category.CreatedAt); err != nil {
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
		respondPaginated(c, categories, total, page)
	}
}

func getCategory(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseCategoryID(c)
		if !ok {
			return
		}

		category, err := findCategory(c, db, id, c.GetInt64(userIDContextKey))
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
			 SET name = $1, type = $2, icon = $3, color = $4
			 WHERE id = $5 AND user_id = $6
			 RETURNING id, name, type, icon, color, created_at`,
			input.Name, input.Type, input.Icon, input.Color, id, c.GetInt64(userIDContextKey),
		).Scan(&category.ID, &category.Name, &category.Type, &category.Icon, &category.Color, &category.CreatedAt)
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
			`DELETE FROM categories WHERE id = $1 AND user_id = $2`,
			id, c.GetInt64(userIDContextKey),
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

func findCategory(c *gin.Context, db *sql.DB, id, userID int64) (Category, error) {
	var category Category
	err := db.QueryRowContext(c.Request.Context(),
		`SELECT id, name, type,
		        COALESCE(icon, CASE type WHEN 'income' THEN '💰' ELSE '📦' END),
		        COALESCE(color, CASE type WHEN 'income' THEN '#10b981' ELSE '#f59e0b' END),
		        created_at
		 FROM categories
		 WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&category.ID, &category.Name, &category.Type, &category.Icon, &category.Color, &category.CreatedAt)
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
	input.Icon = strings.TrimSpace(input.Icon)
	if input.Icon == "" {
		input.Icon = map[string]string{"income": "💰", "expense": "📦"}[input.Type]
	}
	if utf8.RuneCountInString(input.Icon) > 28 {
		return "icon must be at most 28 characters"
	}
	input.Color = strings.TrimSpace(input.Color)
	if input.Color == "" {
		input.Color = map[string]string{"income": "#10b981", "expense": "#f59e0b"}[input.Type]
	}
	if !categoryColorPattern.MatchString(input.Color) {
		return "color must be a 7-character hex color"
	}
	return ""
}

var categoryColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
