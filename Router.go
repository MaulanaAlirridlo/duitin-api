package main

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

func Router() *gin.Engine {
	return gin.Default()
}

func Categories(router *gin.Engine, db *sql.DB) {
	router.POST("/categories", createCategory(db))
	router.GET("/categories", listCategories(db))
	router.GET("/categories/:id", getCategory(db))
	router.PUT("/categories/:id", updateCategory(db))
	router.DELETE("/categories/:id", deleteCategory(db))
}

func Transactions(router *gin.Engine, db *sql.DB) {
	router.POST("/transactions", createTransaction(db))
	router.GET("/transactions", listTransactions(db))
	router.GET("/transactions/:id", getTransaction(db))
	router.PUT("/transactions/:id", updateTransaction(db))
	router.DELETE("/transactions/:id", deleteTransaction(db))
}

func Dashboard(router *gin.Engine, db *sql.DB) {
	router.GET("/dashboard/summary", dashboardSummaryHandler(db))
	router.GET("/dashboard/cashflow", dashboardCashflowHandler(db))
	router.GET("/dashboard/expenses-by-category", expensesByCategoryHandler(db))
	router.GET("/dashboard/recent-transactions", recentTransactionsHandler(db))
}
