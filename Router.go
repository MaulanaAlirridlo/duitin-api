package main

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

func Router() *gin.Engine {
	return gin.Default()
}

func Categories(router *gin.Engine, db *sql.DB, authenticate gin.HandlerFunc) {
	protected := router.Group("", authenticate)
	protected.POST("/categories", createCategory(db))
	protected.GET("/categories", listCategories(db))
	protected.GET("/categories/:id", getCategory(db))
	protected.PUT("/categories/:id", updateCategory(db))
	protected.DELETE("/categories/:id", deleteCategory(db))
}

func Transactions(router *gin.Engine, db *sql.DB, authenticate gin.HandlerFunc) {
	protected := router.Group("", authenticate)
	protected.POST("/transactions", createTransaction(db))
	protected.GET("/transactions", listTransactions(db))
	protected.GET("/transactions/:id", getTransaction(db))
	protected.PUT("/transactions/:id", updateTransaction(db))
	protected.DELETE("/transactions/:id", deleteTransaction(db))
}

func Dashboard(router *gin.Engine, db *sql.DB, authenticate gin.HandlerFunc) {
	protected := router.Group("", authenticate)
	protected.GET("/dashboard/summary", dashboardSummaryHandler(db))
	protected.GET("/dashboard/cashflow", dashboardCashflowHandler(db))
	protected.GET("/dashboard/expenses-by-category", expensesByCategoryHandler(db))
	protected.GET("/dashboard/recent-transactions", recentTransactionsHandler(db))
}
