package main

import (
	"database/sql"
	"encoding/json"
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

type Transaction struct {
	ID              int64       `json:"id"`
	Title           string      `json:"title"`
	Amount          json.Number `json:"amount"`
	Type            string      `json:"type"`
	CategoryID      int64       `json:"category_id"`
	TransactionDate string      `json:"transaction_date"`
	Description     *string     `json:"description"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type transactionInput struct {
	Title           string      `json:"title"`
	Amount          json.Number `json:"amount"`
	Type            string      `json:"type"`
	CategoryID      int64       `json:"category_id"`
	TransactionDate string      `json:"transaction_date"`
	Description     *string     `json:"description"`
}

var transactionAmountPattern = regexp.MustCompile(`^-?[0-9]{1,13}(\.[0-9]{1,2})?$`)

func createTransaction(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input transactionInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if message := validateTransactionInput(&input); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		transaction, err := insertTransaction(c, db, input)
		if err != nil {
			if isCategoryForeignKeyError(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "category_id does not reference an existing category"})
				return
			}
			log.Printf("create transaction: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create transaction"})
			return
		}
		c.JSON(http.StatusCreated, transaction)
	}
}

func insertTransaction(c *gin.Context, db *sql.DB, input transactionInput) (Transaction, error) {
	var transaction Transaction
	var amount string
	var transactionDate time.Time
	err := db.QueryRowContext(c.Request.Context(),
		`INSERT INTO transactions
		(user_id, title, amount, type, category_id, transaction_date, description, created_at, updated_at)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 RETURNING id, title, amount::text, type, category_id, transaction_date, description, created_at, updated_at`,
		c.GetInt64(userIDContextKey), input.Title, string(input.Amount), input.Type, input.CategoryID, input.TransactionDate, input.Description,
	).Scan(
		&transaction.ID, &transaction.Title, &amount, &transaction.Type,
		&transaction.CategoryID, &transactionDate, &transaction.Description,
		&transaction.CreatedAt, &transaction.UpdatedAt,
	)
	if err != nil {
		return Transaction{}, err
	}
	transaction.Amount = json.Number(amount)
	transaction.TransactionDate = transactionDate.Format("2006-01-02")
	return transaction, nil
}

func listTransactions(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, ok := parsePageOptions(c)
		if !ok {
			return
		}
		filters, sortColumn, sortDirection, ok := transactionListFilters(c)
		if !ok {
			return
		}
		var total int64
		if err := db.QueryRowContext(c.Request.Context(),
			"SELECT COUNT(*) FROM transactions"+filters.where(),
			filters.args...,
		).Scan(&total); err != nil {
			log.Printf("count transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not count transactions"})
			return
		}

		rows, err := db.QueryContext(c.Request.Context(),
			`SELECT id, title, amount::text, type, category_id, transaction_date, description, created_at, updated_at
			 FROM transactions`+filters.where()+`
			 ORDER BY `+sortColumn+` `+sortDirection+`, id DESC
			 LIMIT $`+strconv.Itoa(len(filters.args)+1)+` OFFSET $`+strconv.Itoa(len(filters.args)+2),
			filters.argsWith(page.Limit, page.Offset)...,
		)
		if err != nil {
			log.Printf("list transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list transactions"})
			return
		}
		defer rows.Close()

		transactions := make([]Transaction, 0)
		for rows.Next() {
			transaction, err := scanTransaction(rows)
			if err != nil {
				log.Printf("read transaction: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read transactions"})
				return
			}
			transactions = append(transactions, transaction)
		}
		if err := rows.Err(); err != nil {
			log.Printf("iterate transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read transactions"})
			return
		}
		respondPaginated(c, transactions, total, page)
	}
}

func transactionListFilters(c *gin.Context) (sqlFilters, string, string, bool) {
	filters := sqlFilters{}
	filters.add("user_id = ?", c.GetInt64(userIDContextKey))
	if transactionType := strings.TrimSpace(c.Query("type")); transactionType != "" {
		if utf8.RuneCountInString(transactionType) > 20 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "type must be at most 20 characters"})
			return filters, "", "", false
		}
		filters.add("type = ?", transactionType)
	}
	if rawCategoryID := strings.TrimSpace(c.Query("category_id")); rawCategoryID != "" {
		categoryID, err := strconv.ParseInt(rawCategoryID, 10, 64)
		if err != nil || categoryID < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "category_id must be a positive integer"})
			return filters, "", "", false
		}
		filters.add("category_id = ?", categoryID)
	}
	dates, ok := parseDateRange(c, false)
	if !ok {
		return filters, "", "", false
	}
	addDateRange(&filters, "transaction_date", dates)
	filters.addSearch("title", c.Query("search"))
	filters.addSearch("description", c.Query("search"))

	sortColumns := map[string]string{
		"id":               "id",
		"title":            "title",
		"amount":           "amount",
		"type":             "type",
		"transaction_date": "transaction_date",
		"created_at":       "created_at",
	}
	sortKey := strings.TrimSpace(c.DefaultQuery("sort_by", "transaction_date"))
	sortColumn, exists := sortColumns[sortKey]
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort_by must be one of id, title, amount, type, transaction_date, created_at"})
		return filters, "", "", false
	}
	direction := strings.ToUpper(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	if direction != "ASC" && direction != "DESC" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "order must be asc or desc"})
		return filters, "", "", false
	}
	return filters, sortColumn, direction, true
}

func getTransaction(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseTransactionID(c)
		if !ok {
			return
		}

		transaction, err := findTransaction(c, db, id, c.GetInt64(userIDContextKey))
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
			return
		}
		if err != nil {
			log.Printf("get transaction %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get transaction"})
			return
		}
		c.JSON(http.StatusOK, transaction)
	}
}

func updateTransaction(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseTransactionID(c)
		if !ok {
			return
		}

		var input transactionInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if message := validateTransactionInput(&input); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		transaction, err := updateTransactionRecord(c, db, id, input)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
			return
		}
		if err != nil {
			if isCategoryForeignKeyError(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "category_id does not reference an existing category"})
				return
			}
			log.Printf("update transaction %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update transaction"})
			return
		}
		c.JSON(http.StatusOK, transaction)
	}
}

func updateTransactionRecord(c *gin.Context, db *sql.DB, id int64, input transactionInput) (Transaction, error) {
	var transaction Transaction
	var amount string
	var transactionDate time.Time
	err := db.QueryRowContext(c.Request.Context(),
		`UPDATE transactions
		 SET title = $1, amount = $2, type = $3, category_id = $4,
		     transaction_date = $5, description = $6, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $7 AND user_id = $8
		 RETURNING id, title, amount::text, type, category_id, transaction_date, description, created_at, updated_at`,
		input.Title, string(input.Amount), input.Type, input.CategoryID,
		input.TransactionDate, input.Description, id, c.GetInt64(userIDContextKey),
	).Scan(
		&transaction.ID, &transaction.Title, &amount, &transaction.Type,
		&transaction.CategoryID, &transactionDate, &transaction.Description,
		&transaction.CreatedAt, &transaction.UpdatedAt,
	)
	if err != nil {
		return Transaction{}, err
	}
	transaction.Amount = json.Number(amount)
	transaction.TransactionDate = transactionDate.Format("2006-01-02")
	return transaction, nil
}

func deleteTransaction(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseTransactionID(c)
		if !ok {
			return
		}

		result, err := db.ExecContext(c.Request.Context(),
			`DELETE FROM transactions WHERE id = $1 AND user_id = $2`,
			id, c.GetInt64(userIDContextKey),
		)
		if err != nil {
			log.Printf("delete transaction %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete transaction"})
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			log.Printf("check deleted transaction %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete transaction"})
			return
		}
		if rowsAffected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func scanTransaction(scanner interface{ Scan(...any) error }) (Transaction, error) {
	var transaction Transaction
	var amount string
	var transactionDate time.Time
	err := scanner.Scan(
		&transaction.ID, &transaction.Title, &amount, &transaction.Type,
		&transaction.CategoryID, &transactionDate, &transaction.Description,
		&transaction.CreatedAt, &transaction.UpdatedAt,
	)
	if err != nil {
		return Transaction{}, err
	}
	transaction.Amount = json.Number(amount)
	transaction.TransactionDate = transactionDate.Format("2006-01-02")
	return transaction, nil
}

func findTransaction(c *gin.Context, db *sql.DB, id, userID int64) (Transaction, error) {
	row := db.QueryRowContext(c.Request.Context(),
		`SELECT id, title, amount::text, type, category_id, transaction_date, description, created_at, updated_at
		 FROM transactions
		 WHERE id = $1 AND user_id = $2`,
		id, userID,
	)
	return scanTransaction(row)
}

func parseTransactionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return 0, false
	}
	return id, true
}

func validateTransactionInput(input *transactionInput) string {
	input.Title = strings.TrimSpace(input.Title)
	input.Type = strings.TrimSpace(input.Type)

	if input.Title == "" {
		return "title is required"
	}
	if utf8.RuneCountInString(input.Title) > 150 {
		return "title must be at most 150 characters"
	}
	if input.Amount == "" || !transactionAmountPattern.MatchString(string(input.Amount)) {
		return "amount must fit NUMERIC(15,2)"
	}
	if input.Type == "" {
		return "type is required"
	}
	if utf8.RuneCountInString(input.Type) > 20 {
		return "type must be at most 20 characters"
	}
	if input.CategoryID < 1 {
		return "category_id must be a positive integer"
	}
	transactionDate, err := time.Parse("2006-01-02", input.TransactionDate)
	if err != nil || transactionDate.Year() < 1 {
		return "transaction_date must use YYYY-MM-DD format"
	}
	return ""
}

func isCategoryForeignKeyError(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) &&
		postgresError.Code == "23503" &&
		postgresError.ConstraintName == "fk_transaction_category"
}
