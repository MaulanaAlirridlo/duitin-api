package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type dashboardSummary struct {
	IncomeCount  int64       `json:"income_count"`
	ExpenseCount int64       `json:"expense_count"`
	Income       json.Number `json:"income"`
	Expense      json.Number `json:"expense"`
	Net          json.Number `json:"net"`
}

type cashflowEntry struct {
	Date    string      `json:"date"`
	Income  json.Number `json:"income"`
	Expense json.Number `json:"expense"`
	Net     json.Number `json:"net"`
}

type categoryExpense struct {
	CategoryID   int64       `json:"category_id"`
	CategoryName string      `json:"category_name"`
	Amount       json.Number `json:"amount"`
	Count        int64       `json:"transaction_count"`
}

func dashboardSummaryHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		filters, ok := dashboardFilters(c, false)
		if !ok {
			return
		}

		var summary dashboardSummary
		var income, expense, net string
		err := db.QueryRowContext(c.Request.Context(),
			`SELECT
				COUNT(*) FILTER (WHERE type = 'income'),
				COUNT(*) FILTER (WHERE type = 'expense'),
				COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0)::text,
				COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0)::text,
				(COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0) -
				 COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0))::text
			 FROM transactions`+filters.where(),
			filters.args...,
		).Scan(&summary.IncomeCount, &summary.ExpenseCount, &income, &expense, &net)
		if err != nil {
			log.Printf("get dashboard summary: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get dashboard summary"})
			return
		}
		summary.Income = json.Number(income)
		summary.Expense = json.Number(expense)
		summary.Net = json.Number(net)
		c.JSON(http.StatusOK, summary)
	}
}

func dashboardCashflowHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		dates, ok := parseDateRange(c, true)
		if !ok {
			return
		}
		groupBy := strings.TrimSpace(c.DefaultQuery("group_by", "day"))
		groupExpressions := map[string]string{
			"day":   "date_trunc('day', transaction_date::timestamp)::date",
			"week":  "date_trunc('week', transaction_date::timestamp)::date",
			"month": "date_trunc('month', transaction_date::timestamp)::date",
		}
		groupExpression, ok := groupExpressions[groupBy]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_by must be day, week, or month"})
			return
		}
		filters := sqlFilters{}
		filters.add("user_id = ?", c.GetInt64(userIDContextKey))
		addDateRange(&filters, "transaction_date", dates)
		if !addOptionalCategoryFilter(c, &filters) {
			return
		}

		rows, err := db.QueryContext(c.Request.Context(),
			`SELECT `+groupExpression+`,
				COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0)::text,
				COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0)::text,
				(COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0) -
				 COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0))::text
			 FROM transactions`+filters.where()+`
			 GROUP BY 1
			 ORDER BY 1`,
			filters.args...,
		)
		if err != nil {
			log.Printf("get dashboard cashflow: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get dashboard cashflow"})
			return
		}
		defer rows.Close()

		entries := make([]cashflowEntry, 0)
		for rows.Next() {
			var date time.Time
			var income, expense, net string
			if err := rows.Scan(&date, &income, &expense, &net); err != nil {
				log.Printf("read dashboard cashflow: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read dashboard cashflow"})
				return
			}
			entries = append(entries, cashflowEntry{
				Date: date.Format("2006-01-02"), Income: json.Number(income),
				Expense: json.Number(expense), Net: json.Number(net),
			})
		}
		if err := rows.Err(); err != nil {
			log.Printf("iterate dashboard cashflow: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read dashboard cashflow"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"group_by": groupBy, "data": entries})
	}
}

func expensesByCategoryHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, ok := parsePageOptions(c)
		if !ok {
			return
		}
		dates, ok := parseDateRange(c, false)
		if !ok {
			return
		}
		filters := sqlFilters{}
		filters.clauses = append(filters.clauses, "t.type = 'expense'")
		filters.add("t.user_id = ?", c.GetInt64(userIDContextKey))
		addDateRange(&filters, "t.transaction_date", dates)
		filters.addSearch("c.name", c.Query("search"))

		var total int64
		countQuery := `SELECT COUNT(*) FROM (
			SELECT c.id
			FROM categories c
			JOIN transactions t ON t.category_id = c.id AND t.user_id = c.user_id
			` + filters.where() + `
			GROUP BY c.id
		) AS grouped_categories`
		if err := db.QueryRowContext(c.Request.Context(), countQuery, filters.args...).Scan(&total); err != nil {
			log.Printf("count expenses by category: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not count expenses by category"})
			return
		}

		rows, err := db.QueryContext(c.Request.Context(),
			`SELECT c.id, c.name, SUM(t.amount)::text, COUNT(*)
			 FROM categories c
			 JOIN transactions t ON t.category_id = c.id AND t.user_id = c.user_id
			 `+filters.where()+`
			 GROUP BY c.id, c.name
			 ORDER BY SUM(t.amount) DESC, c.id
			 LIMIT $`+strconv.Itoa(len(filters.args)+1)+` OFFSET $`+strconv.Itoa(len(filters.args)+2),
			filters.argsWith(page.Limit, page.Offset)...,
		)
		if err != nil {
			log.Printf("get expenses by category: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get expenses by category"})
			return
		}
		defer rows.Close()

		expenses := make([]categoryExpense, 0)
		for rows.Next() {
			var item categoryExpense
			var amount string
			if err := rows.Scan(&item.CategoryID, &item.CategoryName, &amount, &item.Count); err != nil {
				log.Printf("read expenses by category: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read expenses by category"})
				return
			}
			item.Amount = json.Number(amount)
			expenses = append(expenses, item)
		}
		if err := rows.Err(); err != nil {
			log.Printf("iterate expenses by category: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read expenses by category"})
			return
		}
		respondPaginated(c, expenses, total, page)
	}
}

func recentTransactionsHandler(db *sql.DB) gin.HandlerFunc {
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
			log.Printf("count recent transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not count recent transactions"})
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
			log.Printf("get recent transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get recent transactions"})
			return
		}
		defer rows.Close()

		transactions := make([]Transaction, 0)
		for rows.Next() {
			transaction, err := scanTransaction(rows)
			if err != nil {
				log.Printf("read recent transaction: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read recent transactions"})
				return
			}
			transactions = append(transactions, transaction)
		}
		if err := rows.Err(); err != nil {
			log.Printf("iterate recent transactions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read recent transactions"})
			return
		}
		respondPaginated(c, transactions, total, page)
	}
}

func dashboardFilters(c *gin.Context, defaultDates bool) (sqlFilters, bool) {
	dates, ok := parseDateRange(c, defaultDates)
	if !ok {
		return sqlFilters{}, false
	}
	filters := sqlFilters{}
	filters.add("user_id = ?", c.GetInt64(userIDContextKey))
	addDateRange(&filters, "transaction_date", dates)
	if !addOptionalCategoryFilter(c, &filters) {
		return sqlFilters{}, false
	}
	return filters, true
}

func addOptionalCategoryFilter(c *gin.Context, filters *sqlFilters) bool {
	rawCategoryID := strings.TrimSpace(c.Query("category_id"))
	if rawCategoryID == "" {
		return true
	}
	categoryID, err := strconv.ParseInt(rawCategoryID, 10, 64)
	if err != nil || categoryID < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category_id must be a positive integer"})
		return false
	}
	filters.add("category_id = ?", categoryID)
	return true
}
